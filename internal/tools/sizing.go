package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/drxinfra/rxkb/internal/kb"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// English descriptions of the sizing form fields. A field the site adds later
// still works: it gets the Russian label of the form as its description.
var sizingDesc = map[string]string{
	"ver":             "Directum RX version",
	"users":           "Total number of RX users",
	"active":          "Users working at the same time; 0 means 30% of the total",
	"mobile":          "Users of the mobile app",
	"docsYear":        "Documents created per year",
	"docKB":           "Average document size, KB",
	"years":           "Planning horizon, years",
	"historyGB":       "Archive migrated from the old system, GB: counts for storage, not for load",
	"os":              "linux, or windows (Windows Server: RX as services, no containers)",
	"db":              "pg = PostgreSQL, mssql = Microsoft SQL Server",
	"pgKind":          "pro = Postgres Pro, vanilla = PostgreSQL (only with db=pg)",
	"place":           "vm = containers on virtual machines, k8s = Kubernetes (Linux only)",
	"k8s":             "ns = a namespace in an existing cluster, full = the whole cluster (only with place=k8s)",
	"store":           "s3 = object storage (MinIO or S3 storage), fs = file storage (volume, NFS, SMB)",
	"ha":              "High availability: every layer survives the loss of one node",
	"fts":             "Full-text search (Elasticsearch or OpenSearch)",
	"dcs":             "Document capture service (scanners, mail, folders)",
	"ario":            "Directum Ario (recognition and classification)",
	"arioPages":       "Ario flow, pages per hour (only with ario)",
	"monitoring":      "Monitoring and centralized logs",
	"logDays":         "Log retention, days",
	"backup":          "Backups: volume outside the contour",
	"test":            "Test contour (a fifth of users, a tenth of data)",
	"preprod":         "Pre-production contour (data as in production)",
	"preprodLikeProd": "Pre-production with the same high availability as production",
	"testInCluster":   "Test and pre-production in the same cluster (with place=k8s and k8s=full)",
}

// sizingMax caps numbers far above any real Directum RX installation, so the
// model never meets absurd input.
var sizingMax = map[string]float64{
	"users": 200000, "active": 200000, "mobile": 200000, "docsYear": 100000000, "docKB": 1000000,
	"historyGB": 10000000, "arioPages": 1000000,
}

// sizingSchema builds the input schema from the site's form and defaults, so
// an agent can pass only what the page offers: the link to the calculation then
// opens the same numbers on the site.
func sizingSchema(e *kb.SizingEngine, fields map[string]kb.SizingField) *jsonschema.Schema {
	props := map[string]*jsonschema.Schema{}
	for _, k := range e.Keys {
		f, def := fields[k], e.Defaults[k]
		desc := sizingDesc[k]
		if desc == "" {
			desc = f.Label
		}
		switch {
		case k == "ver":
			vals := make([]any, len(e.Versions))
			for i, v := range e.Versions {
				vals[i] = v
			}
			props[k] = enum(desc, def, vals...)
		case f.Kind == "checkbox":
			d, _ := def.(float64)
			props[k] = boolean(desc, d != 0)
		case f.Kind == "radio":
			vals := make([]any, 0, len(f.Choices))
			_, numeric := def.(float64)
			for _, c := range f.Choices {
				if numeric {
					n, err := strconv.ParseFloat(c.Value, 64)
					if err != nil {
						continue
					}
					vals = append(vals, n)
				} else {
					vals = append(vals, c.Value)
				}
			}
			props[k] = enum(desc, def, vals...)
			if numeric {
				props[k].Type = "integer"
			}
		default:
			d, _ := def.(float64)
			max := sizingMax[k]
			if max == 0 {
				max = 1e9
			}
			props[k] = num(desc, d, f.Min, max, k != "historyGB")
		}
	}
	return obj(props, e.Keys)
}

type sizeRXOut struct {
	Input    map[string]any     `json:"input"`
	Derived  kb.SizingDerived   `json:"derived"`
	Contours []kb.SizingContour `json:"contours"`
	Grand    kb.SizingTotals    `json:"grand"`
	Warnings []kb.SizingWarning `json:"warnings"`
	URL      string             `json:"url"`
	Help     string             `json:"help"`
}

func (t *toolset) addSizeRX(s *mcp.Server) {
	add(s, &mcp.Tool{
		Name:  "size_rx",
		Title: "Directum RX sizing",
		Description: "Servers, vCPU, RAM and disks for a Directum RX installation by role (application servers, RabbitMQ, " +
			"DBMS, document storage, full-text search, Ario, Kubernetes, monitoring, backups) for production, test and " +
			"pre-production. Runs the very model of the drxinfra.ru/sizing.html calculator, so the numbers are the same as " +
			"on the page, and returns a link that opens this calculation there. An estimate for budget and requirements, " +
			"not a load test. Every parameter is optional; omitted ones take the page defaults.",
		InputSchema: sizingSchema(t.k.Sizing(), t.k.Bundle.Sizing.Fields),
	}, func(_ context.Context, in map[string]any) (string, any, error) { return t.sizeRX(in) })
}

func (t *toolset) sizeRX(in map[string]any) (string, any, error) {
	e := t.k.Sizing()
	fields := t.k.Bundle.Sizing.Fields
	inp := map[string]any{}
	for _, k := range e.Keys {
		v, ok := in[k]
		if !ok {
			inp[k] = e.Defaults[k]
			continue
		}
		switch x := v.(type) {
		case bool:
			inp[k] = 0.0
			if x {
				inp[k] = 1.0
			}
		case float64:
			inp[k] = x
		case json.Number:
			f, err := x.Float64()
			if err != nil {
				return "", nil, fmt.Errorf("%s: not a number", k)
			}
			inp[k] = f
		case string:
			inp[k] = x
		default:
			return "", nil, fmt.Errorf("%s: unexpected value", k)
		}
	}
	r, err := e.Compute(inp)
	if err != nil {
		return "", nil, err
	}
	link := t.k.Bundle.Sizing.Page
	if r.Hash != "" {
		link += "#" + r.Hash
	}
	text := r.Text
	// toText ends with «Расчёт: <адрес страницы>», and outside the browser it has
	// no address: the link to the same calculation goes into the footer instead.
	if i := strings.LastIndex(text, "\nРасчёт: "); i >= 0 {
		text = text[:i]
	}
	input := map[string]any{}
	for _, k := range e.Keys {
		if fields[k].Kind == "checkbox" {
			f, _ := inp[k].(float64)
			input[k] = f != 0
		} else {
			input[k] = inp[k]
		}
	}
	out := sizeRXOut{Input: input, Derived: r.Res.D, Contours: r.Res.Contours, Grand: r.Res.Grand, Warnings: r.Res.Warnings,
		URL: link, Help: t.k.Help}
	return strings.TrimRight(text, "\n") + t.footer(link), out, nil
}
