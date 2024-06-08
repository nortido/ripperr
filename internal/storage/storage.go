package storage

import (
	"bufio"
	"context"
	"crypto/sha1"
	"fmt"
	"os"
)

const (
	tempPath = "/tmp/"
)

type Storage interface {
	SaveLocalFile(ctx context.Context, buf []byte) (path string, err error)
	DeleteLocalFile(ctx context.Context, pathname string) (err error)
}

type storage struct {
}

func (s *storage) SaveLocalFile(ctx context.Context, buf []byte) (path string, err error) {
	var (
		f *os.File
		w *bufio.Writer
	)
	filename := fmt.Sprintf("%x.torrent", sha1.Sum(buf))
	path = tempPath + filename
	if f, err = os.Create(path); err != nil {
		return
	}
	defer f.Close()
	w = bufio.NewWriter(f)
	if _, err = w.Write(buf); err != nil {
		return
	}
	if err = w.Flush(); err != nil {
		return
	}
	fmt.Println("Saved file to: ", path)
	return
}

func (s *storage) DeleteLocalFile(ctx context.Context, path string) (err error) {
	if err = os.Remove(path); err != nil {
		return
	}
	fmt.Println("Deleted file from: ", path)
	return
}

func NewStorage() Storage {
	return &storage{}
}
