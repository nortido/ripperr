package web

import (
	"fmt"
	"time"
)

// commonIANATimezones is a curated list for the schedule picker (sorted).
var commonIANATimezones = []string{
	"UTC",
	"Africa/Cairo",
	"America/Anchorage",
	"America/Argentina/Buenos_Aires",
	"America/Chicago",
	"America/Denver",
	"America/Los_Angeles",
	"America/Mexico_City",
	"America/New_York",
	"America/Sao_Paulo",
	"America/Toronto",
	"America/Vancouver",
	"Asia/Almaty",
	"Asia/Anadyr",
	"Asia/Bangkok",
	"Asia/Barnaul",
	"Asia/Chita",
	"Asia/Dubai",
	"Asia/Hong_Kong",
	"Asia/Irkutsk",
	"Asia/Jakarta",
	"Asia/Kamchatka",
	"Asia/Khandyga",
	"Asia/Kolkata",
	"Asia/Krasnoyarsk",
	"Asia/Magadan",
	"Asia/Novokuznetsk",
	"Asia/Novosibirsk",
	"Asia/Omsk",
	"Asia/Sakhalin",
	"Asia/Seoul",
	"Asia/Shanghai",
	"Asia/Singapore",
	"Asia/Srednekolymsk",
	"Asia/Taipei",
	"Asia/Tashkent",
	"Asia/Tbilisi",
	"Asia/Tehran",
	"Asia/Tokyo",
	"Asia/Tomsk",
	"Asia/Ust-Nera",
	"Asia/Vladivostok",
	"Asia/Yakutsk",
	"Asia/Yekaterinburg",
	"Australia/Melbourne",
	"Australia/Perth",
	"Australia/Sydney",
	"Europe/Amsterdam",
	"Europe/Athens",
	"Europe/Astrakhan",
	"Europe/Berlin",
	"Europe/Helsinki",
	"Europe/Istanbul",
	"Europe/Kaliningrad",
	"Europe/Kiev",
	"Europe/Kirov",
	"Europe/London",
	"Europe/Madrid",
	"Europe/Minsk",
	"Europe/Moscow",
	"Europe/Paris",
	"Europe/Prague",
	"Europe/Rome",
	"Europe/Samara",
	"Europe/Saratov",
	"Europe/Simferopol",
	"Europe/Ulyanovsk",
	"Europe/Volgograd",
	"Europe/Warsaw",
	"Pacific/Auckland",
	"Pacific/Honolulu",
}

type timezoneEntry struct {
	ID     string `json:"id"`
	Offset string `json:"offset"`
}

func timezoneEntries() []timezoneEntry {
	entries := make([]timezoneEntry, len(commonIANATimezones))
	for i, id := range commonIANATimezones {
		entries[i] = timezoneEntry{ID: id, Offset: formatTimezoneOffset(id)}
	}
	return entries
}

func formatTimezoneOffset(id string) string {
	loc, err := time.LoadLocation(id)
	if err != nil {
		return "+00:00"
	}
	_, offset := time.Now().In(loc).Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return fmt.Sprintf("%s%02d:%02d", sign, offset/3600, (offset%3600)/60)
}
