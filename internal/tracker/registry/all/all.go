// Package all подключает все встроенные трекеры: каждый пакет регистрирует себя
// в init. Новый трекер добавляется одной строкой импорта здесь и больше нигде.
package all

import (
	_ "trackerr/internal/tracker/client/kinozal"
	_ "trackerr/internal/tracker/client/nnm"
	_ "trackerr/internal/tracker/client/rutor"
	_ "trackerr/internal/tracker/client/rutracker"
	_ "trackerr/internal/tracker/client/teamhd"
)
