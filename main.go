package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"torr/internal/go-qbittorrent"
)

const (
	tempPath = "/tmp/"
	watchTag = "watch"
)

type Entry struct {
	ID        string
	Category  string
	Timestamp int
	Hash      string
	Name      string
}

var index chan *Entry

var torrentclient *qbittorrent.Client

func main() {
	if err := getClient(); err != nil {
		fmt.Println(err)
		return
	}
	ctx := context.Background()

	index = make(chan *Entry)
	getAllTorrents(ctx)
	processTorrents(ctx)

	http.HandleFunc("/readyz", heartbeat)
	if err := http.ListenAndServe(":6410", nil); err != nil {
		fmt.Println(err)
		return
	}
}

func getClient() error {
	s := qbittorrent.Config{
		Host:     "http://192.168.1.200:8068",
		Username: "admin",
		Password: "adminadmin",
	}
	torrentclient = qbittorrent.NewClient(s)

	if err := torrentclient.Login(); err != nil {
		return err
	}
	return nil
}

func heartbeat(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Alive", 200)
}

func getAllTorrents(ctx context.Context) error {
	torrents, err := torrentclient.GetTorrents(qbittorrent.TorrentFilterOptions{Tag: watchTag})
	if err != nil {
		return err
	}
	for _, t := range torrents {
		go func(t qbittorrent.Torrent) {
			var p *qbittorrent.TorrentProperties

			if p, err = torrentclient.GetTorrentProperties(t.Hash); err != nil {
				return
			}
			index <- &Entry{
				ID:        strings.Split(p.Comment, "=")[1],
				Category:  t.Category,
				Timestamp: p.CreationDate,
				Hash:      p.Hash,
				Name:      p.Name,
			}
		}(t)
	}
	return nil
}

func processTorrents(ctx context.Context) {
	for {
		select {
		case v := <-index:
			go func() {
				if err := process(ctx, v); err != nil {
					fmt.Println(err)
				}
			}()
		case <-ctx.Done():
			return
		}
	}
}

func process(ctx context.Context, e *Entry) (err error) {
	var (
		path string
		tb   *bytes.Buffer
	)
	if tb, err = getTorrentFile(ctx, e); err != nil {
		fmt.Println(err)
		return
	}
	rx := regexp.MustCompile(`creation datei(\d{8,10})e4`)
	m := rx.FindStringSubmatch(string([]rune(tb.String())[120:250]))
	ts, _ := strconv.Atoi(m[1])
	fmt.Println(e.Name, e.Timestamp, ts)
	if e.Timestamp == ts {
		if path, err = writeTorrentFile(ctx, tb.Bytes()); err != nil {
			fmt.Println(err)
			return
		}
		if err = submitTorrent(ctx, e, path); err != nil {
			fmt.Println(err)
			return
		}
	}
	return
}

func getTorrentFile(ctx context.Context, e *Entry) (buf *bytes.Buffer, err error) {
	b := bytes.Buffer{}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://rutracker.org/forum/dl.php?t="+e.ID, nil)
	req.Header.Set("Cookie", "bb_session=PLACEHOLDER")
	if err != nil {
		fmt.Println(err)
	}
	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		fmt.Println(err)
		return
	}
	b.ReadFrom(res.Body)
	res.Body.Close()

	buf = &b
	return
}

func writeTorrentFile(ctx context.Context, buf []byte) (path string, err error) {
	var (
		f *os.File
		w *bufio.Writer
	)
	filename := fmt.Sprintf("%x.torrent", sha1.Sum(buf))
	path = tempPath + filename
	if f, err = os.Create(path); err != nil {
		fmt.Println(err)
		return
	}
	w = bufio.NewWriter(f)
	defer f.Close()
	if _, err = w.Write(buf); err != nil {
		fmt.Println(err)
		return
	}
	if err = w.Flush(); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("Saved torrent to: ", path)
	return
}

func submitTorrent(ctx context.Context, e *Entry, path string) (err error) {
	if err = torrentclient.DeleteTorrentsCtx(ctx, []string{e.Hash}, true); err != nil {
		fmt.Println(err)
		return
	}
	opts := qbittorrent.TorrentAddOptions{Tags: watchTag, Category: e.Category}
	err = torrentclient.AddTorrentFromFileCtx(ctx, path, opts.Prepare())
	fmt.Println("Added new torrent: ", e.Name)
	return
}
