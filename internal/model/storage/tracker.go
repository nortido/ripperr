package storage

import "time"

type Tracker struct {
	TypeID      int    `pgx:"tracker_type_id"`
	Name        string `pgx:"tracker_name"`
	Host        string `pgx:"host"`
	Credentials string `pgx:"credentials"`
	Cookies     string `pgx:"cookies"`
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
	UpdatedAt     *time.Time `pgx:"updated_at"`
}
