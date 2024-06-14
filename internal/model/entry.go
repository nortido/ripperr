package model

type Entry struct {
	ID             string
	Category       string
	Tag            string
	AutoManaged    bool
	MaxRatio       float64
	MaxSeedingTime int64
	Timestamp      int64
	Hash           string
	Name           string
	URL            string
}
