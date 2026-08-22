package registry

import (
	"fmt"
	"sort"

	"trackerr/internal/config"
	trackerModel "trackerr/internal/model/tracker"
)

// Def это всё, что ядру нужно знать о трекере: его номер и ключ, значения по
// умолчанию и конструктор парсера. Пакет трекера регистрирует свой Def в init,
// поэтому добавление трекера не трогает ни config, ни service, ни web.
type Def struct {
	ID       int
	Key      string
	Defaults config.TrackerItem
	New      func(t trackerModel.Tracker) trackerModel.ITracker
}

var (
	defs  = make(map[int]Def)
	byKey = make(map[string]int)
)

// Register вызывается из init пакета трекера, то есть до старта main и в один
// поток, поэтому блокировка не нужна, а любая коллизия это ошибка сборки, а не
// рантайма: падаем сразу, чтобы два трекера не делили номер молча.
func Register(d Def) {
	switch {
	case d.ID <= 0:
		panic(fmt.Sprintf("registry: tracker %q has non-positive id %d", d.Key, d.ID))
	case d.Key == "":
		panic(fmt.Sprintf("registry: tracker id %d has empty key", d.ID))
	case d.New == nil:
		panic(fmt.Sprintf("registry: tracker %q has no constructor", d.Key))
	}
	if prev, ok := defs[d.ID]; ok {
		panic(fmt.Sprintf("registry: id %d taken by %q, cannot register %q", d.ID, prev.Key, d.Key))
	}
	if prevID, ok := byKey[d.Key]; ok {
		panic(fmt.Sprintf("registry: key %q taken by id %d", d.Key, prevID))
	}
	defs[d.ID] = d
	byKey[d.Key] = d.ID
}

// All отдаёт трекеры по возрастанию id: этот порядок видит пользователь в UI.
func All() []Def {
	ids := make([]int, 0, len(defs))
	for id := range defs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]Def, 0, len(ids))
	for _, id := range ids {
		out = append(out, defs[id])
	}
	return out
}

func Keys() []string {
	all := All()
	out := make([]string, 0, len(all))
	for _, d := range all {
		out = append(out, d.Key)
	}
	return out
}

func ByID(id int) (Def, bool) {
	d, ok := defs[id]
	return d, ok
}

func ByKey(key string) (Def, bool) {
	id, ok := byKey[key]
	if !ok {
		return Def{}, false
	}
	return defs[id], true
}

// ID возвращает 0 для незнакомого ключа: 0 не бывает валидным номером трекера,
// вызывающему хватает его как признака "нет такого".
func ID(key string) int {
	return byKey[key]
}

func Key(id int) string {
	return defs[id].Key
}

// ConfigDefaults собирает набор настроек трекеров из значений по умолчанию,
// объявленных самими трекерами. Копия на вызов: дальше по ней ходят env и БД.
func ConfigDefaults() config.Tracker {
	out := make(config.Tracker, len(defs))
	for _, d := range All() {
		item := d.Defaults
		item.Mirrors = append([]string(nil), d.Defaults.Mirrors...)
		out[d.Key] = &item
	}
	return out
}
