// Command device-history prints download and upload of one device over all
// LAN parties it attended.
//
//	export LANLEECHER_URL=https://lanleecher.example LANLEECHER_TOKEN=llk_…   # scope read:clients
//	go run ./examples/device-history 02:00:00:00:01:07
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"text/tabwriter"
	"time"
)

type client struct {
	Name     string   `json:"name"`
	Hostname string   `json:"hostname"`
	MACs     []string `json:"macs"`
	Events   []struct {
		Event struct {
			Name      string    `json:"name"`
			StartedAt time.Time `json:"started_at"`
		} `json:"event"`
		BytesDown int64 `json:"bytes_down"`
		BytesUp   int64 `json:"bytes_up"`
	} `json:"events"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: device-history <mac>")
		os.Exit(2)
	}
	base, token := os.Getenv("LANLEECHER_URL"), os.Getenv("LANLEECHER_TOKEN")
	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/clients/mac/"+url.PathEscape(os.Args[1]), nil)
	if err != nil {
		fail(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		fail(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Errors are application/problem+json with a stable "code".
		var p struct{ Code, Detail string }
		json.NewDecoder(resp.Body).Decode(&p)
		fail(fmt.Errorf("%s: %s (%s)", resp.Status, p.Detail, p.Code))
	}
	var c client
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		fail(err)
	}

	fmt.Printf("%s (%s)  MACs: %v\n\n", c.Name, c.Hostname, c.MACs)
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "Event\tDate\tDownload\tUpload\tUp/Down\t")
	var down, up int64
	for _, e := range c.Events {
		ratio := "–"
		if e.BytesDown > 0 {
			ratio = fmt.Sprintf("%.2f", float64(e.BytesUp)/float64(e.BytesDown))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t\n", e.Event.Name, e.Event.StartedAt.Format("2006-01-02"),
			gb(e.BytesDown), gb(e.BytesUp), ratio)
		down += e.BytesDown
		up += e.BytesUp
	}
	fmt.Fprintf(tw, "total\t\t%s\t%s\t\t\n", gb(down), gb(up))
	tw.Flush()
}

func gb(v int64) string { return fmt.Sprintf("%.1f GB", float64(v)/1e9) }

func fail(err error) {
	fmt.Fprintln(os.Stderr, "device-history:", err)
	os.Exit(1)
}
