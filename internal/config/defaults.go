package config

// DefaultApp returns the config with the per-tracker defaults pre-filled;
// envconfig.Process then overwrites whatever the environment defines.
func DefaultApp() App {
	var app App
	app.Tracker = Tracker{
		Rutracker: TrackerItem{
			Name:        "rutracker",
			Domain:      "rutracker.org",
			Publicity:   "private",
			LoginURL:    "https://rutracker.org/forum/login.php",
			DownloadURL: "https://rutracker.org/forum/dl.php?t=",
			HourLimit:   20,
			ProxyType:   1,
			Enabled:     true,
		},
		Kinozal: TrackerItem{
			Name:        "kinozal",
			Domain:      "kinozal.tv",
			Publicity:   "private",
			LoginURL:    "https://kinozal.tv/takelogin.php",
			DownloadURL: "https://kinozal.tv/download.php?id=",
			HourLimit:   30,
			ProxyType:   1,
			Enabled:     true,
		},
		Rutor: TrackerItem{
			Name:        "rutor",
			Domain:      "rutor.is",
			Publicity:   "public",
			DownloadURL: "https://d.rutor.info/download/",
			HourLimit:   30,
			Enabled:     true,
		},
			Name:        			Domain:      			Publicity:   "private",
			HourLimit:   40,
			ProxyType:   1,
			Enabled:     true,
		},
		NNM: TrackerItem{
			Name:        "nnm",
			Domain:      "nnmclub.to",
			Publicity:   "private",
			LoginURL:    "https://nnmclub.to/forum/login.php",
			DownloadURL: "https://nnmclub.to/forum/download.php?id=",
			HourLimit:   25,
			ProxyType:   1,
			Enabled:     true,
		},
		TeamHD: TrackerItem{
			Name:        "teamhd",
			Domain:      "teamhd.org",
			Publicity:   "private",
			DownloadURL: "https://teamhd.org/download.php?id=",
			HourLimit:   25,
			Enabled:     true,
		},
	}
	return app
}
