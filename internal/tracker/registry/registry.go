package registry

import (
	"fmt"
	"sort"

	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
)

type Def struct {
	ID       int
	Key      string
	Defaults config.TrackerItem
	Login    *LoginForm // nil у nnm и teamhd, они пускают по кукам
	New      func(t trackerModel.Tracker) trackerModel.ITracker
}

type LoginForm struct {
	UserField string
	PassField string
	// тело для первой настройки: служебные поля трекера и пустые логин с паролем.
	// В пустую строку логин подставить некуда, а без служебных полей вход не пройдет
	Template string
}

var (
	defs  = make(map[int]Def)
	byKey = make(map[string]int)
)

func Register(d Def) {
	// зовется из init, до main и в один поток, так что без блокировки. Коллизия тут
	// это ошибка сборки, падаем сразу, чтобы два трекера молча не делили номер
	switch {
	case d.ID <= 0:
		panic(fmt.Sprintf("registry: tracker %q has non-positive id %d", d.Key, d.ID))
	case d.Key == "":
		panic(fmt.Sprintf("registry: tracker id %d has empty key", d.ID))
	case d.New == nil:
		panic(fmt.Sprintf("registry: tracker %q has no constructor", d.Key))
	case d.Login != nil && (d.Login.UserField == "" || d.Login.PassField == ""):
		panic(fmt.Sprintf("registry: tracker %q has an incomplete login form", d.Key))
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

func All() []Def {
	ids := make([]int, 0, len(defs))
	for id := range defs {
		ids = append(ids, id)
	}
	// в этом порядке трекеры видит пользователь в UI
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

func ID(key string) int {
	return byKey[key] // 0 значит нет такого, нулевых номеров у трекеров не бывает
}

func Key(id int) string {
	return defs[id].Key
}

func ConfigDefaults() config.Tracker {
	out := make(config.Tracker, len(defs))
	for _, d := range All() {
		// копия на каждый вызов, дальше ее правят env и БД
		item := d.Defaults
		item.Mirrors = append([]string(nil), d.Defaults.Mirrors...)
		out[d.Key] = &item
	}
	return out
}
