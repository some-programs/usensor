package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/alecthomas/template"
	"github.com/md14454/gosensors"
	"github.com/wcharczuk/go-chart"
)

var (
	sensorData = make(map[Key]*Readings, 0)
	sensorMu   sync.Mutex
)

func main() {

	listenAddr := flag.String("l", ":8080", "listen addr")
	flag.Parse()

	go mainSensors()
	http.HandleFunc("/chart", drawChart)
	http.HandleFunc("/config", renderConfig)
	http.HandleFunc("/", renderRoot)
	if err := http.ListenAndServe(*listenAddr, nil); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func drawChart(res http.ResponseWriter, req *http.Request) {
	res.Header().Set("Cache-control", "max-age=0, must-revalidate")
	config := req.URL.Query().Get("config")
	if config == "" {
		panic("no config supplied")
	}
	var conf ChartConfig
	if err := json.Unmarshal([]byte(config), &conf); err != nil {
		panic(err)
	}
	sensorType := SensorTemprature
	if conf.Type == "fanspeed" {
		sensorType = SensorFanSpeed
	}
	filtered := make(map[string]bool, 0)
	for _, v := range conf.Filter {
		filtered[v] = true
	}
	imgWidth := 1000
	if p := req.URL.Query().Get("width"); p != "" {
		i, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			panic(err)
		}
		imgWidth = int(i)
	}
	var ss []chart.TimeSeries
	sensorMu.Lock()
loop:
	for k, vs := range sensorData {
		if k.Type != sensorType {
			continue loop
		}
		if filtered[k.Label] {
			continue loop
		}
		var xvs []time.Time
		var yvs []float64
		if conf.Duration.Duration == 0 {
			xvs = append(xvs, vs.Time...)
			yvs = append(yvs, vs.Value...)
		} else {
			now := time.Now()
			minTime := now.Add(-conf.Duration.Duration)
		durloop:
			for i, v := range vs.Time {
				if v.After(minTime) {
					xvs = append(xvs, vs.Time[i:]...)
					yvs = append(yvs, vs.Value[i:]...)
					break durloop
				}
			}
		}
		ts := chart.TimeSeries{
			Name:    fmt.Sprintf("%v", k.Label),
			XValues: xvs,
			YValues: yvs,
		}
		ss = append(ss, ts)
	}
	sensorMu.Unlock()

	sort.Slice(ss, func(i, j int) bool { return ss[i].Name < ss[j].Name })
	var charts []chart.Series
	for _, v := range ss {
		if conf.AvgPeriod < 1 {
			charts = append(charts, v)
		} else {
			smaSeries := &chart.SMASeries{
				Name:        v.Name,
				Period:      conf.AvgPeriod,
				InnerSeries: v,
			}
			charts = append(charts, smaSeries)
		}
	}
	graph := chart.Chart{
		Width:  imgWidth,
		Height: 800,
		XAxis: chart.XAxis{
			Style: chart.Style{Show: true},
		},
		YAxis: chart.YAxis{
			Style: chart.Style{Show: true},
		},
		Background: chart.Style{
			Padding: chart.Box{
				Top:  20,
				Left: 100,
			},
		},
		Series: charts,
	}
	//note we have to do this as a separate step because we need a reference to graph
	graph.Elements = []chart.Renderable{
		chart.LegendLeft(&graph),
		// chart.LegendThin(&graph),
	}
	// res.Header().Set("Content-Type", "image/png")
	// graph.Render(chart.PNG, res)
	res.Header().Set("Content-Type", "image/svg+xml")
	graph.Render(chart.SVG, res)
}

type Key struct {
	Adapter string
	Name    string
	Label   string
	Type    SensorType
	Source  string
}

type SensorType int

const (
	SensorUnknown SensorType = iota
	SensorTemprature
	SensorFanSpeed
)

func newSensortTypeFromFeatureType(v gosensors.FeatureType) SensorType {
	if v == gosensors.FeatureTypeFan {
		return SensorFanSpeed
	}
	if v == gosensors.FeatureTypeTemp {
		return SensorTemprature
	}
	return SensorUnknown
}

// Reading .
type Readings struct {
	Time  []time.Time
	Value []float64
}

func mainSensors() {
	gosensors.Init()
	defer gosensors.Cleanup()

	chips := gosensors.GetDetectedChips()

	wantedFeatures := map[gosensors.FeatureType]bool{
		gosensors.FeatureTypeTemp: true,
		gosensors.FeatureTypeFan:  true,
	}
	for {
		for i := 0; i < len(chips); i++ {
			chip := chips[i]

			// fmt.Printf("%v\n", chip)
			// fmt.Printf("Adapter: %v\n", chip.AdapterName())
			features := chip.GetFeatures()

			now := time.Now()
		loop:
			for j := 0; j < len(features); j++ {
				feature := features[j]
				if !wantedFeatures[feature.Type] {
					continue loop
				}
				if feature.GetValue() <= 0 {
					continue loop
				}
				key := Key{
					Adapter: chip.AdapterName(),
					Type:    newSensortTypeFromFeatureType(feature.Type),
					Name:    feature.Name,
					Label:   feature.GetLabel(),
					Source:  "libsensor",
				}

				sensorMu.Lock()
				if vs, ok := sensorData[key]; ok {
					vs.Time = append(vs.Time, now)
					vs.Value = append(vs.Value, feature.GetValue())
				} else {
					sensorData[key] = &Readings{
						Value: []float64{feature.GetValue()},
						Time:  []time.Time{now},
					}
				}
				sensorMu.Unlock()

				// if feature.Type == gosensors.FeatureTypeTemp {
				// 	fmt.Printf("FF: %v ('%v'): %.1f %v\n", feature.Name, feature.GetLabel(), feature.GetValue(), feature.Type)

				// subfeatures := feature.GetSubFeatures()

				// for k := 0; k < len(subfeatures); k++ {
				// 	subfeature := subfeatures[k]
				// 	if subfeature.Type == gosensors.SubFeatureTypeTempInput {
				// 		fmt.Printf("SF  %v: %.1f\n", subfeature.Name, subfeature.GetValue())
				// 	}

				// }
				// }
			}

			// fmt.Printf("\n")
		}
		time.Sleep(time.Millisecond * 200)
	}
}

// ChartConfig .
type ChartConfig struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Filter    []string `json:"filter"`
	AvgPeriod int      `json:"avgPeriod"`
	Duration  Duration `json:"duration"`
}
type ChartConfigs []ChartConfig

func (c ChartConfigs) JSON() string {
	data, err := json.MarshalIndent(&c, "", " ")
	if err != nil {
		panic(err)
	}
	return string(data)
}

func (c ChartConfigs) Query() string {
	data, err := json.Marshal(&c)
	if err != nil {
		panic(err)
	}
	return url.QueryEscape(string(data))
}

func (c ChartConfig) Query() string {
	data, err := json.Marshal(&c)
	if err != nil {
		panic(err)
	}
	return url.QueryEscape(string(data))
}

var defaultChartConfig = ChartConfigs{
	{
		Name:      "Tempratures",
		Type:      "temprature",
		Filter:    []string{"AUXTIN0", "AUXTIN1", "AUXTIN2", "AUXTIN3"},
		AvgPeriod: 8,
		Duration:  Duration{5 * time.Minute},
	},
	{
		Name:      "Fan speeds",
		Type:      "fanspeed",
		AvgPeriod: 8,
		Duration:  Duration{5 * time.Minute},
	},
}

const indexTpl = `
<!DOCTYPE html>
<html>
  <head>
    <meta charset="UTF-8">
	  <title>bv</title>
  </head>
  <body style="margin:0px; padding:0px; overflow-x:hidden;">
    {{range .Configs}}
      <p>{{ .Name }}</p>
      <img id="chart1" src="/chart?config={{ .Query }}">
    {{else}}
       <div><strong>no graphs configured</strong></div>
    {{end}}
    <p><a href="/config?configs={{ .Configs.Query }}">config</a></p>
<script>
window.onload = function() {
  function updateImage(img) {
    var u = new URL(img.src);
    var p = u.searchParams;
    p.set("refresh", new Date().getTime());
    p.set("width", window.innerWidth);
    img.src=u.toString();
  }
  function updateImages() {
      // document.getElementsByTagName('img');
      var els = document.getElementsByTagName('img')
      for(var i = 0; i < els.length; i++) {
       updateImage(els[i]);
      }
  }
    updateImages();
    setInterval(updateImages, 1000);
}
</script>

  </body>
</html>`

type templateData struct {
	Configs ChartConfigs
}

func renderRoot(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.New("body").Parse(indexTpl)
	if err != nil {
		panic(err)
	}
	if err := r.ParseForm(); err != nil {
		panic(err)
	}
	configJSON := r.Form.Get("configs")

	var configs ChartConfigs
	if configJSON == "" {
		configs = defaultChartConfig
	} else {
		err := json.Unmarshal([]byte(configJSON), &configs)
		if err != nil {
			panic(err)
		}
	}

	templateData := templateData{
		Configs: configs,
	}
	if err := tmpl.Execute(w, &templateData); err != nil {
		panic(err)
	}
}

const configTpl = `
<!DOCTYPE html>
<html>
  <head>
    <meta charset="UTF-8">
	  <title>bv</title>
  </head>
  <body>
    <form action="/config" method="post">
     <textarea rows="50" cols="100" name="configs">{{ .Configs.JSON }}</textarea>
    <button type="submit">Submit</button>
  </form>
  </body>
</html>`

func renderConfig(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		panic(err)
	}
	configJSON := r.Form.Get("configs")

	var configs ChartConfigs
	if configJSON == "" {
		configs = defaultChartConfig
	} else {
		err := json.Unmarshal([]byte(configJSON), &configs)
		if err != nil {
			panic(err)
		}
	}
	if r.Method == http.MethodPost {
		http.Redirect(w, r, "/?configs="+configs.Query(), http.StatusTemporaryRedirect)
		return
	}
	tmpl, err := template.New("body").Parse(configTpl)
	if err != nil {
		panic(err)
	}
	formdata := templateData{
		Configs: configs,
	}
	if err := tmpl.Execute(w, &formdata); err != nil {
		panic(err)
	}
}

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(b []byte) (err error) {
	if b[0] == '"' {
		sd := string(b[1 : len(b)-1])
		d.Duration, err = time.ParseDuration(sd)
		return
	}

	var id int64
	id, err = json.Number(string(b)).Int64()
	d.Duration = time.Duration(id)

	return
}

func (d Duration) MarshalJSON() (b []byte, err error) {
	return []byte(fmt.Sprintf(`"%s"`, d.String())), nil
}
