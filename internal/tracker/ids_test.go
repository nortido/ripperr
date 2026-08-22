package tracker

import "trackerr/internal/tracker/registry"

// номера трекеров тесты берут из реестра, а не из констант: константы уехали в
// пакеты самих трекеров вместе с остальной их регистрацией
var (
	RutrackerID = registry.ID("rutracker")
	KinozalID   = registry.ID("kinozal")
	RutorID     = registry.ID("rutor")
)
