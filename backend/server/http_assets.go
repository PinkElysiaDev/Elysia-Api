package server

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
)

func serveEmbeddedUI(files fs.FS) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		name := strings.TrimPrefix(c.Param("filepath"), "/")
		if name == "" {
			name = "index.html"
		}
		if !fs.ValidPath(name) {
			c.Status(http.StatusNotFound)
			return
		}
		file, err := files.Open(name)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		transferred := false
		defer func() {
			if !transferred {
				_ = file.Close()
			}
		}()
		stat, err := file.Stat()
		if err != nil || stat.IsDir() {
			c.Status(http.StatusNotFound)
			return
		}
		size := int(stat.Size())
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		c.Header("Accept-Ranges", "bytes")
		status := http.StatusOK
		start, length := 0, size
		// HEAD describes the full representation. Multiple ranges are ignored;
		// this endpoint supports a single byte range, not multipart assembly.
		if value := string(c.GetHeader("Range")); !c.IsHead() && strings.HasPrefix(value, "bytes=") && !strings.Contains(value, ",") {
			first, end, err := app.ParseByteRange([]byte(value), size)
			// Hertz v0.10.6 accepts a zero suffix (bytes=-0) and empty files.
			// Neither identifies a satisfiable byte range.
			if err != nil || first > end {
				c.Header("Content-Range", fmt.Sprintf("bytes */%d", size))
				c.Status(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", first, end, size))
			start, length, status = first, end-first+1, http.StatusPartialContent
		}
		c.Status(status)
		c.Header("Content-Type", contentType)
		if c.IsHead() {
			c.Response.Header.SetContentLength(size)
			c.Response.SkipBody = true
			return
		}
		if start > 0 {
			if seeker, ok := file.(io.Seeker); ok {
				_, err = seeker.Seek(int64(start), io.SeekStart)
			} else {
				_, err = io.CopyN(io.Discard, file, int64(start))
			}
			if err != nil {
				c.String(500, "could not read asset")
				return
			}
		}
		// Hertz streams and closes this reader after the handler returns. Assets
		// never need to be copied into a large pooled response buffer.
		c.SetBodyStream(&assetBody{Reader: io.LimitReader(file, int64(length)), Closer: file}, length)
		transferred = true
	}
}

type assetBody struct {
	io.Reader
	io.Closer
}
