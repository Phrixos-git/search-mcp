package urlutil

import "testing"

func TestNormalize(t *testing.T) {
	got, err := Normalize("HTTPS://Example.COM:443/a/../b/?utm_source=x&keep=a#frag")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://example.com/b/?keep=a"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestNormalizeRejectsInvalid(t *testing.T) {
	for _, in := range []string{"", " ftp://example.com", "ftp://example.com", "https://user:pass@example.com", "https://example.com/" + string(make([]byte, 2049))} {
		if _, err := Normalize(in); err == nil {
			t.Errorf("accepted %q", in)
		}
	}
}

func TestTrackingParameters(t *testing.T) {
	got, err := Normalize("https://x.test/?FBCLID=a&gclid=b&utm_campaign=c&x=1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://x.test/?x=1" {
		t.Fatalf("got %s", got)
	}
}
