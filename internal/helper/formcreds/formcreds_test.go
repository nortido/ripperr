package formcreds

import "testing"

func TestGetDecodesValue(t *testing.T) {
	raw := "login=%D0%B2%D1%85%D0%BE%D0%B4&login_password=secret&login_username=vasya"
	if got := Get(raw, "login_username"); got != "vasya" {
		t.Fatalf("login_username = %q, want vasya", got)
	}
	if got := Get(raw, "login"); got != "вход" {
		t.Fatalf("login = %q, want вход", got)
	}
	if got := Get(raw, "missing"); got != "" {
		t.Fatalf("missing = %q, want empty", got)
	}
}

func TestGetPlusIsSpace(t *testing.T) {
	if got := Get("username=john+doe", "username"); got != "john doe" {
		t.Fatalf("username = %q, want %q", got, "john doe")
	}
}

func TestSetKeepsOrderAndOtherFields(t *testing.T) {
	raw := "login=%D0%B2%D1%85%D0%BE%D0%B4&login_password=old&login_username=vasya"
	got := Set(raw, "login_password", "new pass&x")
	want := "login=%D0%B2%D1%85%D0%BE%D0%B4&login_password=new+pass%26x&login_username=vasya"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestSetAppendsMissingField(t *testing.T) {
	if got := Set("to=%2F", "username", "vasya"); got != "to=%2F&username=vasya" {
		t.Fatalf("got %q", got)
	}
}

func TestSetOnEmptyRaw(t *testing.T) {
	if got := Set("", "username", "vasya"); got != "username=vasya" {
		t.Fatalf("got %q", got)
	}
}

// поле без "=" это не пара, а флаг: трогать его нельзя, иначе правка логина
// молча перепишет чужой кусок строки
func TestSetKeepsBareFlag(t *testing.T) {
	if got := Set("flag&username=old", "username", "new"); got != "flag&username=new" {
		t.Fatalf("got %q", got)
	}
}

func TestSetReplacesOnlyFirstMatch(t *testing.T) {
	if got := Set("u=a&u=b", "u", "c"); got != "u=c&u=b" {
		t.Fatalf("got %q", got)
	}
}

func TestGetOnMalformedEscapeReturnsRaw(t *testing.T) {
	if got := Get("username=%zz", "username"); got != "%zz" {
		t.Fatalf("got %q, want raw %%zz", got)
	}
}
