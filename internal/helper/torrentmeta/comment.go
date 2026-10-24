package torrentmeta

import "bytes"

func SetComment(data []byte, comment string) ([]byte, error) {
	mi, err := load(data)
	if err != nil {
		return nil, err
	}
	mi.Comment = comment
	var buf bytes.Buffer
	if err = mi.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func Comment(data []byte) (string, error) {
	mi, err := load(data)
	if err != nil {
		return "", err
	}
	return mi.Comment, nil
}
