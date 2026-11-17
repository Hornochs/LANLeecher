package transfer

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strings"
	"testing"
)

func validDoc() *Doc {
	end := int64(2000)
	return &Doc{Format: Format, Version: Version, Software: "test",
		Switches: []Switch{{Name: "sw1", Host: "192.0.2.1", Port: 161, Ports: []Port{{IfIndex: 1, Role: "client", Physical: true}}}},
		Devices:  []Device{{Key: "1"}},
		MACs:     []MAC{{MAC: "02-00-00-00-00-01", Device: "1", FirstSeen: 1, LastSeen: 2}},
		Events: []Event{{Slug: "party", Name: "Party", StartedAt: 1000, EndedAt: &end,
			Totals: []Traffic{{Type: SubjectMAC, Subject: "02:00:00:00:00:01", Port: PortRef{"sw1", 1}, Down: 5}}}},
	}
}

func TestRoundtripNormalisesMAC(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, validDoc()); err != nil {
		t.Fatal(err)
	}
	d, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if d.MACs[0].MAC != "02:00:00:00:00:01" {
		t.Fatalf("MAC not normalised: %q", d.MACs[0].MAC)
	}
}

func TestPlainJSONAccepted(t *testing.T) {
	var gz bytes.Buffer
	if err := Write(&gz, validDoc()); err != nil {
		t.Fatal(err)
	}
	r, _ := gzip.NewReader(&gz)
	plain, _ := io.ReadAll(r)
	if _, err := Read(bytes.NewReader(plain)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(d *Doc){
		"format":          func(d *Doc) { d.Format = "other" },
		"newer version":   func(d *Doc) { d.Version = Version + 1 },
		"multicast MAC":   func(d *Doc) { d.MACs[0].MAC = "01:00:5e:00:00:01" },
		"zero MAC":        func(d *Doc) { d.MACs[0].MAC = "00:00:00:00:00:00" },
		"unknown device":  func(d *Doc) { d.MACs[0].Device = "9" },
		"unknown port":    func(d *Doc) { d.Events[0].Totals[0].Port.IfIndex = 7 },
		"unlisted MAC":    func(d *Doc) { d.Events[0].Totals[0].Subject = "02:00:00:00:00:09" },
		"negative":        func(d *Doc) { d.Events[0].Totals[0].Down = -1 },
		"bad slug":        func(d *Doc) { d.Events[0].Slug = "../x" },
		"bad role":        func(d *Doc) { d.Switches[0].Ports[0].Role = "admin" },
		"dup switch":      func(d *Doc) { d.Switches = append(d.Switches, d.Switches[0]) },
		"ends before":     func(d *Doc) { e := int64(1); d.Events[0].EndedAt = &e },
		"long name":       func(d *Doc) { d.Events[0].Name = strings.Repeat("x", 300) },
		"unknown group":   func(d *Doc) { d.Events[0].Totals[0].Type = SubjectPortGroup },
		"unknown subject": func(d *Doc) { d.Events[0].Totals[0].Type = "user" },
	}
	for name, mutate := range cases {
		d := validDoc()
		mutate(d)
		var ve *ValidationError
		if err := d.Validate(); !errors.As(err, &ve) {
			t.Errorf("%s: want validation error, got %v", name, err)
		}
	}
}

func TestReadRejectsGarbage(t *testing.T) {
	for name, in := range map[string]string{
		"empty":         "",
		"not json":      "hello",
		"unknown field": `{"format":"lanleecher-export","version":1,"evil":1}`,
		"broken gzip":   "\x1f\x8b\x08\x00garbage",
	} {
		if _, err := Read(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLimitReader(t *testing.T) {
	l := &limitReader{r: strings.NewReader("abcdef"), n: 3}
	if _, err := io.ReadAll(l); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	l = &limitReader{r: strings.NewReader("abc"), n: 3}
	if b, err := io.ReadAll(l); err != nil || string(b) != "abc" {
		t.Fatalf("exact size: %q %v", b, err)
	}
}
