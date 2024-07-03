package storage

import "time"

type Tracker struct {
	TypeID      int    `pgx:"tracker_type_id"`
	Name        string `pgx:"tracker_name"`
	Domain      string `pgx:"domain"`
	LoginURL    string `pgx:"login_url"`
	DownloadURL string `pgx:"download_url"`
	Credentials string `pgx:"credentials"`
	Cookies     string `pgx:"cookies"`
	Publicity   string `pgx:"publicity"`
	Exists      bool
}

type Item struct {
	URLHash       string     `pgx:"url_hash"`
	TrackerTypeID int        `pgx:"tracker_type_id"`
	TopicID       string     `pgx:"topic_id"`
	Category      string     `pgx:"category_name"`
	Tag           string     `pgx:"item_tag"`
	Name          string     `pgx:"item_name"`
	URL           string     `pgx:"item_url"`
	CreatedAt     *time.Time `pgx:"created_at"`
	RecheckAt     *time.Time `pgx:"recheck_at"`
	Exists        bool
}
