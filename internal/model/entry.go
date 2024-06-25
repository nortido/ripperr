package model

import "time"

type Entry struct {
	Hash           string
	Category       string
	Tag            string
	URL            string
	TopicID        string
	Timestamp      *time.Time
	Name           string
	AutoManaged    bool
	MaxRatio       float64
	MaxSeedingTime int64
	URLHash        string
	Update         bool
}
