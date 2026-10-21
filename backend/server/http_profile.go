package server

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

func serveProfileIndex(ctx context.Context, c *app.RequestContext) {
	var body strings.Builder
	body.WriteString("<html><body><h1>Profiles</h1>")
	for _, p := range pprof.Profiles() {
		fmt.Fprintf(&body, "<a href=\"%s?debug=1\">%s</a> %d<br>\n", p.Name(), p.Name(), p.Count())
	}
	body.WriteString("</body></html>")
	c.Data(200, "text/html; charset=utf-8", []byte(body.String()))
}

func serveProfile(ctx context.Context, c *app.RequestContext) {
	name := c.Param("profile")
	if name == "cmdline" {
		c.Data(200, "text/plain", []byte(strings.Join(os.Args, "\x00")))
		return
	}
	if name == "symbol" {
		var body strings.Builder
		body.WriteString("num_symbols: 1\n")
		for _, pc := range strings.Split(string(c.URI().QueryString()), "+") {
			address, err := strconv.ParseUint(pc, 0, 64)
			if err == nil {
				if fn := runtime.FuncForPC(uintptr(address)); fn != nil {
					fmt.Fprintf(&body, "%s %s\n", pc, fn.Name())
				}
			}
		}
		c.Data(200, "text/plain", []byte(body.String()))
		return
	}
	var output bytes.Buffer
	if name == "profile" || name == "trace" {
		seconds, _ := strconv.ParseFloat(c.Query("seconds"), 64)
		if seconds <= 0 {
			if name == "trace" {
				seconds = 1
			} else {
				seconds = 30
			}
		}
		var err error
		if name == "trace" {
			err = trace.Start(&output)
		} else {
			err = pprof.StartCPUProfile(&output)
		}
		if err != nil {
			c.String(500, "%s", err)
			return
		}
		timer := time.NewTimer(time.Duration(seconds * float64(time.Second)))
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		if name == "trace" {
			trace.Stop()
		} else {
			pprof.StopCPUProfile()
		}
	} else {
		p := pprof.Lookup(name)
		if p == nil {
			c.Status(404)
			return
		}
		if name == "heap" && c.Query("gc") == "1" {
			runtime.GC()
		}
		debug, _ := strconv.Atoi(c.Query("debug"))
		if err := p.WriteTo(&output, debug); err != nil {
			c.String(500, "%s", err)
			return
		}
		if debug > 0 {
			c.Data(200, "text/plain; charset=utf-8", output.Bytes())
			return
		}
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	c.Data(200, "application/octet-stream", output.Bytes())
}
