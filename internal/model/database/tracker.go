package database

type Tracker struct {
	TypeID      int    `pgx:"tracker_type_id"`
	Name        string `pgx:"tracker_name"`
	Host        string `pgx:"host"`
	Credentials string `pgx:"credentials"`
	Cookies     string `pgx:"cookies"`
}
