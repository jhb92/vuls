// +build !scanner

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	c "github.com/future-architect/vuls/config"
	"github.com/future-architect/vuls/models"
	"github.com/future-architect/vuls/report"
	"github.com/future-architect/vuls/scan"
	"github.com/future-architect/vuls/util"
)

// VulsHandler is used for vuls server mode
type VulsHandler struct {
	DBclient report.DBClient
}

// ServeHTTP is http handler
func (h VulsHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var err error
	result := models.ScanResult{ScannedCves: models.VulnInfos{}}

	contentType := req.Header.Get("Content-Type")
	mediatype, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		util.Log.Error(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if mediatype == "application/json" {
		if err = json.NewDecoder(req.Body).Decode(&result); err != nil {
			util.Log.Error(err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	} else if mediatype == "text/plain" {
		buf := new(bytes.Buffer)
		if _, err := io.Copy(buf, req.Body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if result, err = scan.ViaHTTP(req.Header, buf.String()); err != nil {
			util.Log.Error(err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		util.Log.Error(mediatype)
		http.Error(w, fmt.Sprintf("Invalid Content-Type: %s", contentType), http.StatusUnsupportedMediaType)
		return
	}

	if err := report.DetectPkgCves(h.DBclient, &result); err != nil {
		util.Log.Errorf("Failed to detect Pkg CVE: %+v", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	if err := report.FillCveInfo(h.DBclient, &result); err != nil {
		util.Log.Errorf("Failed to fill CVE detailed info: %+v", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	// set ReportedAt to current time when it's set to the epoch, ensures that ReportedAt will be set
	// properly for scans sent to vuls when running in server mode
	if result.ReportedAt.IsZero() {
		result.ReportedAt = time.Now()
	}

	// report
	reports := []report.ResultWriter{
		report.HTTPResponseWriter{Writer: w},
	}
	if c.Conf.ToLocalFile {
		scannedAt := result.ScannedAt
		if scannedAt.IsZero() {
			scannedAt = time.Now().Truncate(1 * time.Hour)
			result.ScannedAt = scannedAt
		}
		dir, err := scan.EnsureResultDir(scannedAt)
		if err != nil {
			util.Log.Errorf("Failed to ensure the result directory: %+v", err)
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}

		// sever subcmd doesn't have diff option
		reports = append(reports, report.LocalFileWriter{
			CurrentDir: dir,
			FormatJSON: true,
		})
	}

	for _, w := range reports {
		if err := w.Write(result); err != nil {
			util.Log.Errorf("Failed to report. err: %+v", err)
			return
		}
	}
}
