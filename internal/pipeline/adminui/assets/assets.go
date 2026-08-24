package assets

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed css/app.css upload.js vendor/htmx.min.js vendor/fonts/*.woff2 vendor/icons.svg
var files embed.FS

type Paths struct {
	CSS              string
	HTMX             string
	Upload           string
	Icons            string
	GeistRegular     string
	GeistMedium      string
	GeistSemibold    string
	GeistMonoRegular string
}

type Bundle struct {
	Paths   Paths
	content map[string]asset
}

type asset struct {
	contentType string
	data        []byte
}

func New() (*Bundle, error) {
	bundle := &Bundle{content: make(map[string]asset)}
	load := func(name string) (string, error) {
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return "", err
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(data))[:16]
		extension := filepath.Ext(name)
		path := "/static/" + strings.TrimSuffix(filepath.Base(name), extension) + "-" + hash + extension
		contentType := mime.TypeByExtension(extension)
		if extension == ".woff2" {
			contentType = "font/woff2"
		} else if extension == ".svg" {
			contentType = "image/svg+xml"
		}
		bundle.content[path] = asset{contentType: contentType, data: data}
		return path, nil
	}
	var err error
	for target, name := range map[*string]string{
		&bundle.Paths.CSS: "css/app.css", &bundle.Paths.HTMX: "vendor/htmx.min.js", &bundle.Paths.Upload: "upload.js", &bundle.Paths.Icons: "vendor/icons.svg",
		&bundle.Paths.GeistRegular: "vendor/fonts/geist-regular.woff2", &bundle.Paths.GeistMedium: "vendor/fonts/geist-medium.woff2",
		&bundle.Paths.GeistSemibold: "vendor/fonts/geist-semibold.woff2", &bundle.Paths.GeistMonoRegular: "vendor/fonts/geist-mono-regular.woff2",
	} {
		if *target, err = load(name); err != nil {
			return nil, fmt.Errorf("load embedded asset %s: %w", name, err)
		}
	}
	return bundle, nil
}

func (b *Bundle) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		item, ok := b.content[request.URL.Path]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		writer.Header().Set("Content-Type", item.contentType)
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = writer.Write(item.data)
	})
}
