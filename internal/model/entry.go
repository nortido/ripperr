package model

type Entry struct {
	Hash           string
	Category       string
	Tag            string
	URL            string
	TopicID        string
	Timestamp      int64
	Name           string
	AutoManaged    bool
	MaxRatio       float64
	MaxSeedingTime int64
	URLHash        string
	Update         bool
}
