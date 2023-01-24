package main

import (
	"encoding/json"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v2"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	namespace = "ecoflow"
)

type Ecoflow struct {
	Description  string `yaml:"description"`
	SerialNumber string `yaml:"serialNumber"`
	AppKey       string `yaml:"appKey"`
	SecretKey    string `yaml:"secretKey"`
}

type EcoflowExporter struct {
	ecoflow      *Ecoflow
	checkTimeout time.Duration
	mutex        sync.RWMutex
	checkError   prometheus.Gauge
	soc          prometheus.Gauge
	remaintime   prometheus.Gauge
	wattsoutsum  prometheus.Gauge
	wattsinsum   prometheus.Gauge
}

type EcoflowApi struct {
	Code    string
	Message string
	Data    EcoflowApiData
}

type EcoflowApiData struct {
	Soc         float64
	RemainTime  float64
	WattsOutSum float64
	WattsInSum  float64
}

func (params *Ecoflow) defaults() {
	if params.Description == "" {
		params.Description = params.SerialNumber
	}
}

func CreateExporters(ecoflow Ecoflow, checkTimeout time.Duration) (*EcoflowExporter, error) {
	return &EcoflowExporter{
		ecoflow:      &ecoflow,
		checkTimeout: checkTimeout,

		soc: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "soc",
			Help:        "State of charge",
			ConstLabels: prometheus.Labels{"description": fmt.Sprintf("%s", ecoflow.Description), "sn": fmt.Sprintf("%s", ecoflow.SerialNumber)},
		}),

		remaintime: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "remain_time",
			Help:        "Remain time",
			ConstLabels: prometheus.Labels{"description": fmt.Sprintf("%s", ecoflow.Description), "sn": fmt.Sprintf("%s", ecoflow.SerialNumber)},
		}),

		wattsoutsum: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "watts_out_sum",
			Help:        "Current wats output",
			ConstLabels: prometheus.Labels{"description": fmt.Sprintf("%s", ecoflow.Description), "sn": fmt.Sprintf("%s", ecoflow.SerialNumber)},
		}),

		wattsinsum: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "watts_in_sum",
			Help:        "Current wats input",
			ConstLabels: prometheus.Labels{"description": fmt.Sprintf("%s", ecoflow.Description), "sn": fmt.Sprintf("%s", ecoflow.SerialNumber)},
		}),

		checkError: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "check_error",
			Help:        "check error",
			ConstLabels: prometheus.Labels{"description": fmt.Sprintf("%s", ecoflow.Description), "sn": fmt.Sprintf("%s", ecoflow.SerialNumber)},
		}),
	}, nil
}

func (ecoflow *EcoflowExporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- ecoflow.soc.Desc()
	ch <- ecoflow.remaintime.Desc()
	ch <- ecoflow.wattsinsum.Desc()
	ch <- ecoflow.wattsoutsum.Desc()
	ch <- ecoflow.checkError.Desc()
}

func (ecoflow *EcoflowExporter) Collect(ch chan<- prometheus.Metric) {
	ecoflow.mutex.Lock()
	defer func() {
		ch <- ecoflow.soc
		ch <- ecoflow.remaintime
		ch <- ecoflow.wattsinsum
		ch <- ecoflow.wattsoutsum
		ch <- ecoflow.checkError
		ecoflow.mutex.Unlock()
	}()

	res, err := getEcoflowApiData(ecoflow.ecoflow, ecoflow.checkTimeout)

	if err != nil || "0" != res.Code {
		ecoflow.checkError.Set(float64(1))
		return
	}

	ecoflow.soc.Set(res.Data.Soc)
	ecoflow.remaintime.Set(res.Data.RemainTime)
	ecoflow.wattsinsum.Set(res.Data.WattsInSum)
	ecoflow.wattsoutsum.Set(res.Data.WattsOutSum)
}

func getEcoflowApiData(ecoflow *Ecoflow, checkTimeout time.Duration) (EcoflowApi, error) {
	// TODO: get url from args/env
	url := fmt.Sprintf("https://api.ecoflow.com/iot-service/open/api/device/queryDeviceQuota?sn=%s", ecoflow.SerialNumber)
	httpClient := http.Client{
		Timeout: checkTimeout, // Timeout after 2 seconds
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		log.Fatal(err)
	}

	req.Header.Set("User-Agent", "prometheus-ecoflow-exporter")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("appKey", ecoflow.AppKey)
	req.Header.Set("secretKey", ecoflow.SecretKey)

	res, getErr := httpClient.Do(req)
	if getErr != nil {
		return EcoflowApi{}, getErr
	}

	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(res.Body)

	body, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		return EcoflowApi{}, readErr
	}

	var ecoflowData EcoflowApi
	jsonErr := json.Unmarshal(body, &ecoflowData)
	if jsonErr != nil {
		return EcoflowApi{}, jsonErr
	}

	return ecoflowData, nil
}

func main() {

	var listen string
	listenDefault := "0.0.0.0:9136"
	pflag.StringVar(&listen, "listen", listenDefault, "Listen address. Env LISTEN also can be used.")

	var configFile string
	configFileDefault := "/etc/prometheus/prometheus-ecoflow-exporter.yaml"
	pflag.StringVar(&configFile, "config-file", configFileDefault, "Config file")

	var metricsPath string
	metricsPathDefault := "/metrics"
	pflag.StringVar(&metricsPath, "metrics-path", metricsPathDefault, "Metrics path")

	var checkTimeout time.Duration
	checkTimeoutDefault := 5 * time.Second
	pflag.DurationVar(&checkTimeout, "check_timeout", checkTimeoutDefault, "Check timeout")

	pflag.Parse()

	if listen == listenDefault && len(os.Getenv("LISTEN")) > 0 {
		listen = os.Getenv("LISTEN")
	}

	if configFile == configFileDefault && len(os.Getenv("CONFIG_FILE")) > 0 {
		configFile = os.Getenv("CONFIG_FILE")
	}

	if metricsPath == metricsPathDefault && len(os.Getenv("METRICS_PATH")) > 0 {
		metricsPath = os.Getenv("METRICS_PATH")
	}

	if checkTimeout == checkTimeoutDefault && len(os.Getenv("CHECK_TIMEOUT")) > 0 {
		var err error
		checkTimeout, err = time.ParseDuration(os.Getenv("CHECK_TIMEOUT"))
		if err != nil {
			panic(err)
		}
	}

	var ecoflowListConfig = make([]Ecoflow, 256)
	var ecoflowList = make(map[string]Ecoflow, 256)

	config, err := os.ReadFile(configFile)
	if err != nil {
		log.Fatal("Couldn't read config: ", err)
	}

	err = yaml.Unmarshal(config, &ecoflowListConfig)
	if err != nil {
		log.Fatal("Couldn't parse config: ", err)
	}

	for ecoflow := range ecoflowListConfig {
		if _, ok := ecoflowList[ecoflowListConfig[ecoflow].SerialNumber]; !ok {
			t := ecoflowListConfig[ecoflow]
			t.defaults()
			ecoflowList[ecoflowListConfig[ecoflow].SerialNumber] = t
		}
	}

	for _, ecoflow := range ecoflowList {
		exporter, err := CreateExporters(ecoflow, checkTimeout)
		if err != nil {
			log.Fatal(err)
		}
		prometheus.MustRegister(exporter)
	}

	log.Printf("Statring ecoflow exporter on %s", listen)

	http.Handle(metricsPath, promhttp.Handler())
	err = http.ListenAndServe(listen, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
