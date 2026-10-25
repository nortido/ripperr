package torrentmeta

import "strings"

func FilePaths(data []byte) ([]string, error) {
	mi, err := load(data)
	if err != nil {
		return nil, err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{info.BestName()}, nil
	}
	out := make([]string, 0, len(info.Files))
	for _, f := range info.Files {
		// qbit не показывает pad-файлы BEP47 в списке файлов
		if strings.Contains(f.Attr, "p") {
			continue
		}
		out = append(out, info.BestName()+"/"+strings.Join(f.BestPath(), "/"))
	}
	return out, nil
}
