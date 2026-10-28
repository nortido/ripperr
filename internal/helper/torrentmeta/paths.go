package torrentmeta

import "strings"

func FilePaths(data []byte) ([]string, error) {
	paths, _, err := FileSizes(data)
	return paths, err
}

func FileSizes(data []byte) ([]string, []int64, error) {
	mi, err := load(data)
	if err != nil {
		return nil, nil, err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() {
		return []string{info.BestName()}, []int64{info.Length}, nil
	}
	paths := make([]string, 0, len(info.Files))
	sizes := make([]int64, 0, len(info.Files))
	for _, f := range info.Files {
		// qbit не показывает pad-файлы BEP47 в списке файлов
		if strings.Contains(f.Attr, "p") {
			continue
		}
		paths = append(paths, info.BestName()+"/"+strings.Join(f.BestPath(), "/"))
		sizes = append(sizes, f.Length)
	}
	return paths, sizes, nil
}
