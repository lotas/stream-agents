package server

import (
	"context"
	"html"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"stream-agents/internal/store"
)

var searchTmpl = template.Must(template.New("").Funcs(funcMap).ParseFS(tmplFS, "templates/layout.html", "templates/search.html"))

const (
	searchMaxResults  = 100
	searchMaxSnippets = 3
	searchWorkers     = 8
)

type searchSnippet struct {
	Role string
	Body template.HTML // escaped text with <mark> around matched terms
}

type searchResult struct {
	Session  store.Session
	Hits     int // messages containing at least one term
	Snippets []searchSnippet
}

type searchData struct {
	PageTitle   string
	Query       string
	AgentFilter string
	Results     []searchResult
	Total       int // matched sessions before truncation
	Scanned     int
}

// searchTerms splits a query into lowercase, deduplicated keywords.
func searchTerms(q string) []string {
	seen := map[string]bool{}
	var terms []string
	for _, f := range strings.Fields(strings.ToLower(q)) {
		if !seen[f] {
			seen[f] = true
			terms = append(terms, f)
		}
	}
	return terms
}

func messageSearchText(m store.Message) string {
	text := m.Text
	if input, ok := m.Meta["input"].(string); ok && input != "" {
		text += "\n" + input
	}
	return text
}

// matchSession reports whether every term occurs somewhere in the session
// (title, project, or any message), collecting snippets from matching messages.
func matchSession(sess store.Session, msgs []store.Message, terms []string) (searchResult, bool) {
	res := searchResult{Session: sess}
	found := make(map[string]bool, len(terms))
	markFound := func(lower string) bool {
		hit := false
		for _, t := range terms {
			if strings.Contains(lower, t) {
				found[t] = true
				hit = true
			}
		}
		return hit
	}
	markFound(strings.ToLower(sess.Title + "\n" + sess.Project))
	for _, m := range msgs {
		text := messageSearchText(m)
		lower := strings.ToLower(text)
		if !markFound(lower) {
			continue
		}
		res.Hits++
		if len(res.Snippets) < searchMaxSnippets {
			res.Snippets = append(res.Snippets, searchSnippet{Role: m.Role, Body: snippet(text, lower, terms)})
		}
	}
	return res, len(found) == len(terms)
}

// snippet returns an escaped excerpt around the first matched term with all
// term occurrences wrapped in <mark>. lower must be strings.ToLower(text).
func snippet(text, lower string, terms []string) template.HTML {
	// ToLower can change byte lengths for some runes; fall back to the lowered
	// text so offsets stay valid.
	if len(lower) != len(text) {
		text = lower
	}
	first := -1
	for _, t := range terms {
		if i := strings.Index(lower, t); i >= 0 && (first < 0 || i < first) {
			first = i
		}
	}
	if first < 0 {
		first = 0
	}
	start, end := first-80, first+160
	if start < 0 {
		start = 0
	}
	if end > len(text) {
		end = len(text)
	}
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	excerpt, lowerEx := text[start:end], lower[start:end]

	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	for i := 0; i < len(excerpt); {
		matched := 0
		for _, t := range terms {
			if strings.HasPrefix(lowerEx[i:], t) && len(t) > matched {
				matched = len(t)
			}
		}
		if matched > 0 {
			b.WriteString("<mark>" + html.EscapeString(excerpt[i:i+matched]) + "</mark>")
			i += matched
			continue
		}
		_, size := utf8.DecodeRuneInString(excerpt[i:])
		b.WriteString(html.EscapeString(excerpt[i : i+size]))
		i += size
	}
	if end < len(text) {
		b.WriteString("…")
	}
	return template.HTML(strings.Join(strings.Fields(b.String()), " "))
}

// searchSessions loads every session concurrently and returns those matching
// all terms, most recently modified first.
func searchSessions(ctx context.Context, idx *store.Index, sessions []store.Session, terms []string) []searchResult {
	var (
		mu      sync.Mutex
		results []searchResult
		wg      sync.WaitGroup
		jobs    = make(chan store.Session)
	)
	for range searchWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sess := range jobs {
				msgs, err := idx.LoadSession(ctx, sess.Agent, sess.ID)
				if err != nil {
					continue
				}
				if res, ok := matchSession(sess, msgs, terms); ok {
					mu.Lock()
					results = append(results, res)
					mu.Unlock()
				}
			}
		}()
	}
	for _, s := range sessions {
		if ctx.Err() != nil {
			break
		}
		jobs <- s
	}
	close(jobs)
	wg.Wait()
	sort.Slice(results, func(i, j int) bool {
		return results[i].Session.Modified.After(results[j].Session.Modified)
	})
	return results
}

func (h *handlers) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	agentFilter := r.URL.Query().Get("agent")
	data := searchData{PageTitle: "Search — stream-agents", Query: q, AgentFilter: agentFilter}
	if terms := searchTerms(q); len(terms) > 0 {
		data.PageTitle = q + " — search — stream-agents"
		sessions, err := h.idx.ListAll(r.Context(), agentFilter, "")
		if err != nil {
			http.Error(w, "failed to list sessions: "+err.Error(), 500)
			return
		}
		data.Scanned = len(sessions)
		data.Results = searchSessions(r.Context(), h.idx, sessions, terms)
		data.Total = len(data.Results)
		if len(data.Results) > searchMaxResults {
			data.Results = data.Results[:searchMaxResults]
		}
	}
	if err := searchTmpl.ExecuteTemplate(w, "search.html", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
