package server

import (
	"fmt"
	"time"

	"stream-agents/internal/store"
)

type activityDay struct {
	Date, Detail string
	Level        int
	Outside      bool
}
type activityWeek struct {
	Month string
	Days  []activityDay
}
type activityHeatmap struct {
	Year, Sessions, ActiveDays int
	Weeks                      []activityWeek
}

func buildHeatmap(sessions []store.Session, year int) activityHeatmap {
	h := activityHeatmap{Year: year}
	daily := map[string]*statsRow{}
	max := 0
	for _, s := range sessions {
		date := s.Started
		if date.IsZero() {
			date = s.Modified
		}
		date = date.UTC()
		if date.Year() != year {
			continue
		}
		key := date.Format("2006-01-02")
		if daily[key] == nil {
			daily[key] = &statsRow{}
		}
		daily[key].add(s)
		if daily[key].Sessions > max {
			max = daily[key].Sessions
		}
		h.Sessions++
	}
	h.ActiveDays = len(daily)
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 0, 0)
	cursor := start.AddDate(0, 0, -int(start.Weekday()))
	for cursor.Before(end) {
		week := activityWeek{}
		for day := 0; day < 7; day++ {
			cell := activityDay{Date: cursor.Format("2006-01-02"), Outside: cursor.Before(start) || !cursor.Before(end)}
			if !cell.Outside {
				if cursor.Day() == 1 {
					week.Month = cursor.Format("Jan")
				}
				row := daily[cell.Date]
				if row == nil {
					row = &statsRow{}
				}
				if row.Sessions > 0 {
					cell.Level = 1 + (row.Sessions*4-1)/max
					if cell.Level > 4 {
						cell.Level = 4
					}
				}
				cell.Detail = fmt.Sprintf("%s: %d sessions, %d tokens, %d agent types", cell.Date, row.Sessions, row.Tokens, row.Agents)
			}
			week.Days = append(week.Days, cell)
			cursor = cursor.AddDate(0, 0, 1)
		}
		h.Weeks = append(h.Weeks, week)
	}
	return h
}
