package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"stream-agents/internal/report"
)

// runStats implements `stream-agents stats`: a usage report printed to stdout.
func runStats(args []string) {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s stats [flags]\n\nPrints token usage and estimated cost by period, agent, provider and model.\n\n", os.Args[0])
		fs.PrintDefaults()
	}
	newIndex := storeFlags(fs)
	group := fs.String("group", "day", "period grouping: day, month or year")
	from := fs.String("from", "", "first day to include, YYYY-MM-DD (local time)")
	to := fs.String("to", "", "last day to include, YYYY-MM-DD (local time)")
	days := fs.Int("days", 0, "only include the last N days, including today (overrides -from)")
	agent := fs.String("agent", "", "only include this agent (claude, codex, opencode)")
	project := fs.String("project", "", "only include sessions for this project path")
	by := fs.String("by", strings.Join(report.AllSections, ","), "comma-separated sections to show: "+strings.Join(report.AllSections, ", "))
	breakdown := fs.Bool("breakdown", false, "list per-model usage under each period")
	asJSON := fs.Bool("json", false, "print JSON instead of tables")
	fs.Parse(args)

	fail := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "stats: "+format+"\n", a...)
		os.Exit(2)
	}
	if fs.NArg() > 0 {
		fail("unexpected argument %q", fs.Arg(0))
	}
	if *group != "day" && *group != "month" && *group != "year" {
		fail("invalid -group %q: use day, month or year", *group)
	}
	opts := report.Options{Group: *group, Agent: *agent, Project: *project, Breakdown: *breakdown}
	for _, section := range strings.Split(*by, ",") {
		section = strings.TrimSpace(section)
		if !slices.Contains(report.AllSections, section) {
			fail("invalid -by section %q: use %s", section, strings.Join(report.AllSections, ", "))
		}
		opts.Sections = append(opts.Sections, section)
	}
	for _, field := range []struct {
		name, value string
		date        *time.Time
	}{{"from", *from, &opts.From}, {"to", *to, &opts.To}} {
		if field.value == "" {
			continue
		}
		parsed, err := time.ParseInLocation("2006-01-02", field.value, time.Local)
		if err != nil {
			fail("invalid -%s %q: use YYYY-MM-DD", field.name, field.value)
		}
		*field.date = parsed
	}
	if *days < 0 {
		fail("-days must be positive")
	}
	if *days > 0 {
		now := time.Now()
		opts.From = time.Date(now.Year(), now.Month(), now.Day()-*days+1, 0, 0, 0, 0, time.Local)
	}
	if !opts.From.IsZero() && !opts.To.IsZero() && opts.From.After(opts.To) {
		fail("-from must not be after -to")
	}

	idx := newIndex()
	opts.CostMultipliers = idx.CostMultipliers
	sessions, err := idx.ListAll(context.Background(), "", "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "stats: failed to list sessions:", err)
		os.Exit(1)
	}
	rep := report.Build(sessions, opts, time.Local)
	if *asJSON {
		err = report.WriteJSON(os.Stdout, rep, opts.Sections)
	} else {
		err = report.WriteText(os.Stdout, rep, opts.Sections)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "stats:", err)
		os.Exit(1)
	}
}
