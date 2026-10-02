package helper

import (
	"errors"
	"testing"
)

func TestIsFlareTimeoutRecognizesChallengeSolveFailure(t *testing.T) {
	err := errors.New("flaresolverr error: Error: Error solving the challenge. Message: invalid cookie domain (Session info: chrome=137.0.7151.103)")
	if !IsFlareTimeout(err) {
		t.Fatal("expected challenge solve failure to be recognized")
	}
}

func TestIsFlareTimeoutRecognizesSolveTimeout(t *testing.T) {
	// функция ловит и исчерпание отведенного времени, а у него своя логика в shouldReloginAfterFlare
	err := errors.New("flaresolverr error: Timeout after 60000ms")
	if !IsFlareTimeout(err) {
		t.Fatal("expected solve timeout to be recognized")
	}
}

func TestIsFlareTimeoutIgnoresUnrelatedError(t *testing.T) {
	err := errors.New("flaresolverr error: unexpected status code 502")
	if IsFlareTimeout(err) {
		t.Fatal("unrelated flaresolverr error must not be recognized")
	}
}

func TestIsFlareTimeoutHandlesNil(t *testing.T) {
	if IsFlareTimeout(nil) {
		t.Fatal("nil error must not be recognized")
	}
}
