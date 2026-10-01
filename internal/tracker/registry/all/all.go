// Package all подключает все встроенные трекеры: каждый пакет регистрирует себя
// в init. Новый трекер добавляется одной строкой импорта здесь и больше нигде.
package all

import (
	_ "ripperr/internal/tracker/client/kinozal"
	_ "ripperr/internal/tracker/client/nnm"
	_ "ripperr/internal/tracker/client/rutor"
	_ "ripperr/internal/tracker/client/rutracker"
	_ "ripperr/internal/tracker/client/teamhd"
)
