package provider

import (
	"context"
	"testing"
	"time"
)

const sampleBoxRecSchedule = `{
  "provider": "boxrec",
  "fetchedAt": "2026-08-07T09:32:56.958Z",
  "events": [
    {
      "headerText": "Fri 7, Aug • Gimnasio Municipal Nº 1, Trelew event",
      "date": "Fri 7, Aug",
      "dateId": "2026-08-07",
      "eventId": "956455",
      "eventName": null,
      "location": "Gimnasio Municipal Nº 1, Trelew",
      "bouts": [
        {
          "uid": "3686998",
          "cells": ["Sofia Ayelen Antieco", "debut", "VS", "4x2", "Mirian Basan", "debut", "Box-pro", "light fly", "bout"],
          "links": [
            {"text": "Sofia Ayelen Antieco", "href": "/en/box-pro/1135226"},
            {"text": "Mirian Basan", "href": "/en/box-pro/1484676"},
            {"text": "bout", "href": "/en/event/956455/3686998"}
          ]
        },
        {
          "uid": "3678709",
          "cells": ["#237", "Mohammad Salman", "8 0 2", "VS", "4", "TBA", "Box-pro", "bantam", "bout"],
          "links": [
            {"text": "Mohammad Salman", "href": "/en/box-pro/1207989"},
            {"text": "bout", "href": "/en/event/955633/3678709"}
          ]
        }
      ]
    },
    {
      "headerText": "Fri 7, Aug • Dhaka, Bangladesh event",
      "date": "Fri 7, Aug",
      "dateId": "2026-08-07",
      "eventId": "955633",
      "eventName": null,
      "location": "Dhaka, Bangladesh",
      "bouts": [
        {
          "uid": "3678715",
          "cells": ["#2657", "Mohd Rayian Hossain", "1 0 0", "VS", "3", "Harman Singh", "debut", "Box-am", "welter", "bout"],
          "links": [
            {"text": "Mohd Rayian Hossain", "href": "/en/box-am/1454174"},
            {"text": "Harman Singh", "href": "/en/box-am/1481007"},
            {"text": "bout", "href": "/en/event/955633/3678715"}
          ]
        }
      ]
    }
  ]
}`

func TestParseBoxRecScheduleConvertsResolvedBout(t *testing.T) {
	fixtures, err := ParseBoxRecSchedule([]byte(sampleBoxRecSchedule))
	if err != nil {
		t.Fatalf("ParseBoxRecSchedule() error = %v", err)
	}
	if len(fixtures) != 2 {
		t.Fatalf("ParseBoxRecSchedule() fixtures = %d, want 2 (TBA bout skipped)", len(fixtures))
	}

	pro := fixtures[0]
	wantScheduledAt := time.Date(2026, time.August, 7, 0, 0, 0, 0, time.UTC)
	if pro.SourceType != "boxrec" || pro.SourceID != "956455:3686998" || pro.League != "boxing" {
		t.Errorf("fixture identity = %#v", pro)
	}
	if !pro.ScheduledAt.Equal(wantScheduledAt) || pro.ScheduledAt.Location() != time.UTC {
		t.Errorf("ScheduledAt = %v (%v), want %v (UTC)", pro.ScheduledAt, pro.ScheduledAt.Location(), wantScheduledAt)
	}
	if pro.State != StatePending {
		t.Errorf("State = %q, want %q", pro.State, StatePending)
	}
	if pro.Away != (Team{Name: "Sofia Ayelen Antieco", Role: "away"}) {
		t.Errorf("Away = %#v", pro.Away)
	}
	if pro.Home != (Team{Name: "Mirian Basan", Role: "home"}) {
		t.Errorf("Home = %#v", pro.Home)
	}
}

func TestParseBoxRecScheduleSkipsTBABout(t *testing.T) {
	fixtures, err := ParseBoxRecSchedule([]byte(sampleBoxRecSchedule))
	if err != nil {
		t.Fatalf("ParseBoxRecSchedule() error = %v", err)
	}
	for _, f := range fixtures {
		if f.SourceID == "956455:3678709" {
			t.Errorf("TBA bout was not skipped: %#v", f)
		}
	}
}

func TestParseBoxRecScheduleHandlesAmateurLinks(t *testing.T) {
	fixtures, err := ParseBoxRecSchedule([]byte(sampleBoxRecSchedule))
	if err != nil {
		t.Fatalf("ParseBoxRecSchedule() error = %v", err)
	}
	amateur := fixtures[1]
	if amateur.SourceID != "955633:3678715" {
		t.Fatalf("amateur bout source id = %q, want 955633:3678715", amateur.SourceID)
	}
	if amateur.Away.Name != "Mohd Rayian Hossain" || amateur.Home.Name != "Harman Singh" {
		t.Errorf("amateur fighters = %#v vs %#v", amateur.Away, amateur.Home)
	}
}

func TestParseBoxRecScheduleRejectsInvalidJSON(t *testing.T) {
	if _, err := ParseBoxRecSchedule([]byte("{not json")); err == nil {
		t.Fatal("ParseBoxRecSchedule() accepted invalid JSON")
	}
}

func TestParseBoxRecScheduleSkipsEventWithoutDateID(t *testing.T) {
	bad := `{"provider":"boxrec","fetchedAt":"x","events":[{"headerText":"no date","dateId":"","eventId":"1","bouts":[{"uid":"2","cells":["a","VS","b"],"links":[{"text":"a","href":"/en/box-pro/1"},{"text":"b","href":"/en/box-pro/2"}]}]}]}`
	fixtures, err := ParseBoxRecSchedule([]byte(bad))
	if err != nil {
		t.Fatalf("ParseBoxRecSchedule() error = %v", err)
	}
	if len(fixtures) != 0 {
		t.Errorf("fixtures = %d, want 0 (no date id)", len(fixtures))
	}
}

func TestStaticBoxRecSourceReturnsFixturesForAnyDay(t *testing.T) {
	source, err := NewBoxRecSource([]byte(sampleBoxRecSchedule))
	if err != nil {
		t.Fatalf("NewBoxRecSource() error = %v", err)
	}
	day := time.Date(2026, time.August, 8, 0, 0, 0, 0, time.UTC)
	fixtures, err := source.Fixtures(context.Background(), day)
	if err != nil {
		t.Fatalf("Fixtures() error = %v", err)
	}
	if len(fixtures) != 2 {
		t.Errorf("Fixtures() = %d, want 2", len(fixtures))
	}
}
