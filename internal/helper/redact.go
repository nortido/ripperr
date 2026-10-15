package helper

import (
	"errors"
	"net/url"
	"strings"
)

const redacted = "***"

func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return redacted
	}
	out := u.Scheme + "://" + u.Host
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		out += "/" + redacted
	}
	return out
}

func RedactURLError(err error) error {
	var ue *url.Error
	if err == nil || !errors.As(err, &ue) || ue.URL == "" {
		return err
	}
	old := ue.URL
	ue.URL = RedactURL(old)
	if err == error(ue) {
		return err
	}
	// fmt.Errorf запоминает текст обертки при создании, правка *url.Error его уже
	// не меняет. Пересобираем текст, цепочку сохраняем
	return &redactedError{msg: strings.ReplaceAll(err.Error(), old, ue.URL), err: err}
}

type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }
