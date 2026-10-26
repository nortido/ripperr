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

func TestExpandEscapesValues(t *testing.T) {
	body := "u={login}&p={password}&x=1"
	if got, want := Expand(body, "a&b=c+d%e{password}", "{login} }"), "u=a%26b%3Dc%2Bd%25e%7Bpassword%7D&p=%7Blogin%7D+%7D&x=1"; got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if got, want := Expand(body, "", "pw"), "u=&p=pw&x=1"; got != want {
		t.Fatalf("empty login: got %q, want %q", got, want)
	}
	if got := Expand("a=1&b=2", "l", "p"); got != "a=1&b=2" {
		t.Fatalf("no placeholders: got %q", got)
	}
	if got := Expand("", "l", "p"); got != "" {
		t.Fatalf("empty body: got %q", got)
	}
}

func TestTemplatize(t *testing.T) {
	const u, p = "login_username", "login_password"
	tests := []struct {
		name         string
		body         string
		wantTmpl     string
		wantLogin    string
		wantPassword string
		wantHasLogin bool
		wantHasPass  bool
	}{
		{
			name:     "registry template",
			body:     "login_username=&login_password=&login=%D0%B2%D1%85%D0%BE%D0%B4",
			wantTmpl: "login_username={login}&login_password={password}&login=%D0%B2%D1%85%D0%BE%D0%B4",
		},
		{
			name:         "all occurrences masked, first real value returned",
			body:         "a=1&login_username=va+sya&login_password=p%40&login_password=zz",
			wantTmpl:     "a=1&login_username={login}&login_password={password}&login_password={password}",
			wantLogin:    "va sya",
			wantPassword: "p@",
			wantHasLogin: true,
			wantHasPass:  true,
		},
		{
			name:     "existing template unchanged",
			body:     "login_username={login}&login_password={password}&x=1",
			wantTmpl: "login_username={login}&login_password={password}&x=1",
		},
		{
			name:         "missing user field appended",
			body:         "login_password=x&a=1",
			wantTmpl:     "login_password={password}&a=1&login_username={login}",
			wantPassword: "x",
			wantHasPass:  true,
		},
		{
			name:         "placeholder under another name blocks append",
			body:         "user={login}&login_password=x",
			wantTmpl:     "user={login}&login_password={password}",
			wantPassword: "x",
			wantHasPass:  true,
		},
		{
			name:         "malformed escape returned raw",
			body:         "login_username=%zz&login_password=",
			wantTmpl:     "login_username={login}&login_password={password}",
			wantLogin:    "%zz",
			wantHasLogin: true,
		},
		{
			name:     "empty body",
			body:     "",
			wantTmpl: "login_username={login}&login_password={password}",
		},
		{
			name:         "bare part is not the field",
			body:         "login_username&login_password=x",
			wantTmpl:     "login_username&login_password={password}&login_username={login}",
			wantPassword: "x",
			wantHasPass:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, login, password, hasLogin, hasPass := Templatize(tt.body, u, p)
			if tmpl != tt.wantTmpl || login != tt.wantLogin || password != tt.wantPassword ||
				hasLogin != tt.wantHasLogin || hasPass != tt.wantHasPass {
				t.Fatalf("got (%q, %q, %q, %v, %v)\nwant (%q, %q, %q, %v, %v)",
					tmpl, login, password, hasLogin, hasPass,
					tt.wantTmpl, tt.wantLogin, tt.wantPassword, tt.wantHasLogin, tt.wantHasPass)
			}
		})
	}
}

func TestTemplatizeExpandRoundTrip(t *testing.T) {
	forms := []struct{ name, tmpl, user, pass string }{
		{"rutracker", "login_username=&login_password=&login=%D0%B2%D1%85%D0%BE%D0%B4", "login_username", "login_password"},
		{"kinozal", "username=&password=&to=%2F", "username", "password"},
		{"toloka", "username=&password=&autologin=on&ssl=on&redirect=&login=%D0%92%D1%85%D1%96%D0%B4", "username", "password"},
		{"tapochek", "login_username=&login_password=&autologin=1&redirect=index.php&login=1", "login_username", "login_password"},
		{"booktracker", "login_username=&login_password=&autologin=1&redirect=index.php&login=1", "login_username", "login_password"},
		{"newstudio", "login_username=&login_password=&autologin=1&redirect=index.php&login=1", "login_username", "login_password"},
		{"riperam", "username=&password=&autologin=on&login=%D0%92%D1%85%D0%BE%D0%B4", "username", "password"},
		{"anidub", "login_name=&login_password=&login=submit", "login_name", "login_password"},
		{"animelayer", "login=&password=", "login", "password"},
		{"selezen", "login_name=&login_password=&login=submit", "login_name", "login_password"},
	}
	creds := []struct{ login, password string }{
		{"vasya", "secret"},
		{"a&b=c+d%e {x}", "p&q=r+s%t {password}"},
		{"{login}", "{password} {login}"},
		{"вася пупкин", "пароль&1=2"},
	}
	for _, f := range forms {
		for _, c := range creds {
			b := Set(Set(f.tmpl, f.user, c.login), f.pass, c.password)
			tmpl, login, password, hasLogin, hasPass := Templatize(b, f.user, f.pass)
			if !hasLogin || !hasPass || login != c.login || password != c.password {
				t.Fatalf("%s %q: got (%q, %q, %v, %v)", f.name, c.login, login, password, hasLogin, hasPass)
			}
			if got := Expand(tmpl, login, password); got != b {
				t.Fatalf("%s %q: expand\ngot  %q\nwant %q", f.name, c.login, got, b)
			}
		}
	}
}
