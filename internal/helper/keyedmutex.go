package helper

import "sync"

type KeyedMutex struct {
	m sync.Map
}

func (k *KeyedMutex) Mutex(key any) *sync.Mutex {
	v, _ := k.m.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (k *KeyedMutex) Delete(key any) {
	k.m.Delete(key)
}
