// Package tray shows a menu bar item with today's token usage and cost
// estimate, linking into the web UI.
package tray

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"

	"stream-agents/internal/server"
	"stream-agents/internal/store"
)

//go:embed icon.png
var icon []byte

const refreshInterval = time.Minute

// maxAgents is the number of per-agent menu slots under each period.
const maxAgents = 4

// Run blocks running the tray until the user picks Quit. It must be called
// from the main goroutine.
func Run(idx *store.Index, baseURL string) {
	systray.Run(func() { onReady(idx, baseURL) }, nil)
}

// link is a menu item that opens a URL when clicked. Its title and URL can be
// updated on refresh; an empty URL hides it.
type link struct {
	item *systray.MenuItem
	mu   sync.Mutex
	url  string
}

func newLink(title, url string) *link {
	return watchLink(systray.AddMenuItem(title, ""), title, url)
}

func newSubLink(parent *systray.MenuItem, title, url string) *link {
	return watchLink(parent.AddSubMenuItem(title, ""), title, url)
}

func watchLink(item *systray.MenuItem, title, url string) *link {
	l := &link{item: item}
	l.set(title, url)
	go func() {
		for range l.item.ClickedCh {
			l.mu.Lock()
			u := l.url
			l.mu.Unlock()
			if u != "" {
				openURL(u)
			}
		}
	}()
	return l
}

func (l *link) set(title, url string) {
	l.mu.Lock()
	l.url = url
	l.mu.Unlock()
	l.item.SetTitle(title)
	if url == "" {
		l.item.Hide()
	} else {
		l.item.Show()
	}
}

func onReady(idx *store.Index, baseURL string) {
	systray.SetTemplateIcon(icon, icon)
	systray.SetTooltip("stream-agents")

	today := newLink("Today", baseURL+"/stats")
	var agents []*link
	for range maxAgents {
		agents = append(agents, newLink("", ""))
	}
	systray.AddSeparator()
	// Longer periods list their per-agent breakdown in a submenu. A parent
	// item that opens a submenu isn't clickable itself, so the submenu
	// repeats the all-agents link first.
	type periodMenu struct {
		item   *systray.MenuItem
		all    *link
		agents []*link
	}
	var periods []periodMenu
	for range 4 {
		m := periodMenu{item: systray.AddMenuItem("", "")}
		m.all = newSubLink(m.item, "", "")
		for range maxAgents {
			m.agents = append(m.agents, newSubLink(m.item, "", ""))
		}
		periods = append(periods, m)
	}
	systray.AddSeparator()
	activeHeader := systray.AddMenuItem("Active sessions", "")
	activeHeader.Disable()
	var active []*link
	for range 8 {
		active = append(active, newLink("", ""))
	}
	systray.AddSeparator()
	newLink("Open Sessions", baseURL+"/")
	newLink("Open Stats", baseURL+"/stats")
	newLink("Open Search", baseURL+"/search")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "")
	go func() {
		<-quit.ClickedCh
		systray.Quit()
	}()

	refresh := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s, err := server.LoadSummary(ctx, idx)
		if err != nil {
			log.Printf("tray: %v", err)
			systray.SetTitle(" !")
			systray.SetTooltip(err.Error())
			return
		}
		now := time.Now()
		systray.SetTitle(" " + fmtCost(s.Today.SummaryUsage, true) + " / " + fmtCost(s.Week.SummaryUsage, true))
		systray.SetTooltip("stream-agents — updated " + now.Format("15:04"))
		today.set("Today: "+fmtUsage(s.Today.SummaryUsage), statsURL(baseURL, "", s.Today))
		setAgents(agents, "    ", baseURL, s.Today)
		for i, p := range []struct {
			label  string
			period server.SummaryPeriod
		}{
			{"Last 7 days", s.Week},
			{"This month", s.Month},
			{"Last month", s.LastMonth},
			{"Last 3 months", s.Last3Months},
		} {
			periods[i].item.SetTitle(p.label + ": " + fmtUsage(p.period.SummaryUsage))
			periods[i].all.set("All agents: "+fmtUsage(p.period.SummaryUsage), statsURL(baseURL, "", p.period))
			setAgents(periods[i].agents, "", baseURL, p.period)
		}
		if len(s.Active) == 0 {
			activeHeader.Hide()
		} else {
			activeHeader.Show()
		}
		for i, l := range active {
			if i < len(s.Active) {
				a := s.Active[i]
				l.set(truncate(a.Title, 50)+" ("+a.Agent+")", baseURL+a.URL)
			} else {
				l.set("", "")
			}
		}
	}
	go func() {
		refresh()
		for range time.Tick(refreshInterval) {
			refresh()
		}
	}()
}

// setAgents fills links with p's per-agent usage, hiding unused slots.
func setAgents(links []*link, indent, baseURL string, p server.SummaryPeriod) {
	for i, l := range links {
		if i < len(p.Agents) {
			a := p.Agents[i]
			l.set(indent+a.Agent+": "+fmtUsage(a.SummaryUsage), statsURL(baseURL, a.Agent, p))
		} else {
			l.set("", "")
		}
	}
}

func statsURL(baseURL, agent string, p server.SummaryPeriod) string {
	q := url.Values{"group": {"day"}, "from": {p.From}, "to": {p.To}}
	if agent != "" {
		q.Set("agent", agent)
	}
	return baseURL + "/stats?" + q.Encode()
}

func fmtUsage(u server.SummaryUsage) string {
	sessions := "sessions"
	if u.Sessions == 1 {
		sessions = "session"
	}
	return fmt.Sprintf("%s tokens · %s · %d %s", fmtTokens(u.Tokens), fmtCost(u, false), u.Sessions, sessions)
}

// fmtTokens formats a token count compactly, e.g. 950, 12K, 1.2M.
func fmtTokens(n int) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0fK", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// fmtCost formats an estimated cost; short drops cents above $10. A trailing
// "+" marks that some usage had no known price.
func fmtCost(u server.SummaryUsage, short bool) string {
	s := fmt.Sprintf("$%.2f", u.Cost)
	if short && u.Cost >= 10 {
		s = fmt.Sprintf("$%.0f", u.Cost)
	}
	if u.CostPartial {
		s += "+"
	}
	return s
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func openURL(u string) {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	if err := exec.Command(cmd, u).Run(); err != nil {
		log.Printf("tray: open %s: %v", u, err)
	}
}
