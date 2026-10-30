package main

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"time"
)

// writeLog writes n access-log lines spread over the last three days for
// users*30 distinct accounts and a few thousand destinations. It exists to
// measure ingestion throughput.
func writeLog(path string, n, users int, seed int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	defer w.Flush()
	r := rand.New(rand.NewSource(seed))
	hosts := make([]string, 4000)
	for i := range hosts {
		hosts[i] = fmt.Sprintf("cdn%d.site%d.example.com", i%7, i)
	}
	for _, c := range []string{"chatgpt.com", "www.youtube.com", "i.instagram.com", "web.telegram.org", "github.com"} {
		hosts = append(hosts, c)
	}
	start := time.Now().Add(-72 * time.Hour)
	span := 72 * 3600.0
	nu := users * 30
	for i := 0; i < n; i++ {
		ts := start.Add(time.Duration(float64(i) / float64(n) * span * float64(time.Second)))
		u := r.Intn(nu)
		fmt.Fprintf(w, "%s from %d.%d.%d.%d:%d accepted tcp:%s:443 [inbound-443 >> direct] email: user%d@mail\n",
			ts.Format("2006/01/02 15:04:05.000000"), 5+u%200, u%250, (u/7)%250, 1+u%250, 20000+r.Intn(40000), hosts[r.Intn(len(hosts))], u)
	}
	return nil
}
