package kb

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dop251/goja"
)

// The sizing calculator of drxinfra.ru/sizing.html keeps its whole model in
// assets/sizing.js (RXSizing.compute). The server runs that very script in an
// embedded JavaScript interpreter, so every number matches the site by
// construction. The script comes from the site repository at build time, never
// from a request; a request only supplies validated numbers and enum values.

const sizingTimeout = time.Second

// The glue calls the site's API and returns everything as one JSON string, so
// numbers are printed by JavaScript itself.
const sizingGlue = `(function (api) {
  return function (inp) {
    var res = api.compute(inp);
    return JSON.stringify({ res: res, text: api.toText(res), hash: api.encode(inp) });
  };
})(RXSizing)`

// SizingEngine runs RXSizing.compute from the site's sizing.js.
type SizingEngine struct {
	prog     *goja.Program
	glue     *goja.Program
	pool     chan *sizingVM
	Versions []string       // RXSizing.VERSIONS, newest first
	Keys     []string       // Object.keys(RXSizing.DEFAULTS): order of the form and of the URL
	Defaults map[string]any // RXSizing.DEFAULTS
}

type sizingVM struct {
	rt  *goja.Runtime
	run goja.Callable
}

// NewSizingEngine compiles the script and checks that it exposes the API the
// server relies on.
func NewSizingEngine(script string) (*SizingEngine, error) {
	prog, err := goja.Compile("sizing.js", script, false)
	if err != nil {
		return nil, err
	}
	glue, err := goja.Compile("glue.js", sizingGlue, false)
	if err != nil {
		return nil, err
	}
	e := &SizingEngine{prog: prog, glue: glue, pool: make(chan *sizingVM, 4)}
	vm, err := e.newVM()
	if err != nil {
		return nil, err
	}
	meta, err := vm.rt.RunString(`JSON.stringify({v: RXSizing.VERSIONS, k: Object.keys(RXSizing.DEFAULTS), d: RXSizing.DEFAULTS})`)
	if err != nil {
		return nil, err
	}
	var m struct {
		V []string       `json:"v"`
		K []string       `json:"k"`
		D map[string]any `json:"d"`
	}
	if err := json.Unmarshal([]byte(meta.String()), &m); err != nil {
		return nil, err
	}
	if len(m.V) == 0 || len(m.K) == 0 {
		return nil, errors.New("RXSizing.VERSIONS или RXSizing.DEFAULTS пустые")
	}
	e.Versions, e.Keys, e.Defaults = m.V, m.K, m.D
	if _, err := e.runOn(vm, e.Defaults); err != nil {
		if errors.Is(err, errTimerFired) {
			return e, nil
		}
		return nil, fmt.Errorf("расчёт по умолчанию: %w", err)
	}
	e.put(vm)
	return e, nil
}

func (e *SizingEngine) newVM() (*sizingVM, error) {
	rt := goja.New()
	rt.SetMaxCallStackSize(512)
	if _, err := rt.RunProgram(e.prog); err != nil {
		return nil, err
	}
	api := rt.Get("RXSizing")
	if api == nil || goja.IsUndefined(api) {
		return nil, errors.New("скрипт не объявил RXSizing")
	}
	for _, name := range []string{"compute", "toText", "encode"} {
		if _, ok := goja.AssertFunction(api.ToObject(rt).Get(name)); !ok {
			return nil, fmt.Errorf("нет функции RXSizing.%s", name)
		}
	}
	g, err := rt.RunProgram(e.glue)
	if err != nil {
		return nil, err
	}
	run, ok := goja.AssertFunction(g)
	if !ok {
		return nil, errors.New("glue не функция")
	}
	return &sizingVM{rt: rt, run: run}, nil
}

func (e *SizingEngine) get() (*sizingVM, error) {
	select {
	case vm := <-e.pool:
		return vm, nil
	default:
		return e.newVM()
	}
}

func (e *SizingEngine) put(vm *sizingVM) {
	select {
	case e.pool <- vm:
	default:
	}
}

// Compute runs the model on a complete input (all keys of Defaults present,
// numbers as float64, the rest strings) and returns the site's result.
func (e *SizingEngine) Compute(inp map[string]any) (*SizingResult, error) {
	vm, err := e.get()
	if err != nil {
		return nil, err
	}
	res, err := e.runOn(vm, inp)
	if errors.Is(err, errTimerFired) {
		return res, nil // a good result from a VM that is not reused
	}
	if err != nil {
		return nil, err // the VM may be interrupted half way: do not reuse it
	}
	e.put(vm)
	return res, nil
}

var errTimerFired = errors.New("timer fired")

func (e *SizingEngine) runOn(vm *sizingVM, inp map[string]any) (*SizingResult, error) {
	obj := vm.rt.NewObject()
	for _, k := range e.Keys {
		v, ok := inp[k]
		if !ok {
			v = e.Defaults[k]
		}
		if err := obj.Set(k, v); err != nil {
			return nil, err
		}
	}
	timer := time.AfterFunc(sizingTimeout, func() { vm.rt.Interrupt("timeout") })
	out, err := vm.run(goja.Undefined(), obj)
	if !timer.Stop() && err == nil {
		// The interrupt may land after the call returned: the result is fine,
		// but the VM must not go back to the pool with a pending interrupt.
		var r SizingResult
		if jerr := json.Unmarshal([]byte(out.String()), &r); jerr != nil {
			return nil, jerr
		}
		return &r, errTimerFired
	}
	if err != nil {
		var ie *goja.InterruptedError
		if errors.As(err, &ie) {
			return nil, errors.New("расчёт не уложился во время")
		}
		return nil, err
	}
	var r SizingResult
	if err := json.Unmarshal([]byte(out.String()), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// SizingResult is what RXSizing.compute, toText and encode return.
type SizingResult struct {
	Res  SizingRes `json:"res"`
	Text string    `json:"text"` // RXSizing.toText: the same text the site copies to the clipboard
	Hash string    `json:"hash"` // RXSizing.encode: the URL fragment that opens this calculation on the site
}

// SizingRes mirrors the object returned by RXSizing.compute.
type SizingRes struct {
	Inp      map[string]any  `json:"inp"`
	D        SizingDerived   `json:"d"`
	Contours []SizingContour `json:"contours"`
	Grand    SizingTotals    `json:"grand"`
	Warnings []SizingWarning `json:"warnings"`
}

// SizingDerived holds the values the model derives from the input.
type SizingDerived struct {
	U         float64 `json:"U"`
	T         float64 `json:"T"`
	M         float64 `json:"M"`
	StoreGB   float64 `json:"storeGB"`
	DbGB      float64 `json:"dbGB"`
	DocsTotal float64 `json:"docsTotal"`
}

// SizingContour is one contour: production, pre-production or test.
type SizingContour struct {
	Kind     string        `json:"kind"`
	Title    string        `json:"title"`
	U        float64       `json:"U"`
	T        float64       `json:"T"`
	M        float64       `json:"M"`
	HA       bool          `json:"ha"`
	Nodes    []SizingNode  `json:"nodes"`
	K8sQuota *SizingQuota  `json:"k8sQuota"`
	Totals   SizingTotals  `json:"totals"`
	BackupGB float64       `json:"backupGB"`
	DbGB     float64       `json:"dbGB"`
	StoreGB  float64       `json:"storeGB"`
	Logs     SizingLogs    `json:"logs"`
	Notes    []interface{} `json:"notes"`
}

// SizingNode is one server role.
type SizingNode struct {
	Role        string       `json:"role"`
	N           float64      `json:"n"`
	CPU         float64      `json:"cpu"`
	RAM         float64      `json:"ram"`
	Disk        []SizingDisk `json:"disk"`
	DiskTotal   float64      `json:"diskTotal"`
	SW          string       `json:"sw"`
	Note        string       `json:"note"`
	K8s         bool         `json:"k8s"`
	StorageOnly bool         `json:"storageOnly"`
}

// SizingDisk is one disk of a node, in GB.
type SizingDisk struct {
	Name string  `json:"name"`
	GB   float64 `json:"gb"`
}

// SizingQuota is the Kubernetes namespace quota.
type SizingQuota struct {
	ReqCPU float64 `json:"reqCpu"`
	ReqRAM float64 `json:"reqRam"`
	LimCPU float64 `json:"limCpu"`
	LimRAM float64 `json:"limRam"`
	PV     float64 `json:"pv"`
	Pods   string  `json:"pods"`
}

// SizingTotals sums virtual machines, vCPU, RAM and disks (GB).
type SizingTotals struct {
	VMs    float64 `json:"vms"`
	CPU    float64 `json:"cpu"`
	RAM    float64 `json:"ram"`
	Disk   float64 `json:"disk"`
	Backup float64 `json:"backup,omitempty"`
}

// SizingLogs is the log volume of a contour.
type SizingLogs struct {
	PerDay    float64 `json:"perDay"`
	Days      float64 `json:"days"`
	GB        float64 `json:"gb"`
	MetricsGB float64 `json:"metricsGb"`
}

// SizingWarning is a remark of the model: lvl is "warn" or "info".
type SizingWarning struct {
	Lvl  string `json:"lvl"`
	Text string `json:"text"`
}
