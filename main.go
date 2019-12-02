package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
)

var addr = flag.String("listen-address", "0.0.0.0:9601", "The address to listen on for HTTP requests.")
var interval = flag.Int("interval", 300, "Interval (in seconds) for request balance.")
var retryInterval = flag.Int("retry-interval", 10, "Interval (in seconds) for load balance when errors.")
var retryLimit = flag.Int("retry-limit", 10, "Count of tries when error.")

var (
	ctx          *mcsContext
	credentials  = CredentialsConfig{}
	balanceGauge *prometheus.GaugeVec
	hasError     = false
	retryCount   = 0
)

type BalanceResponse struct {
	Balance   string `json:"balance"`
	ErrorCode int    `json:"error_code"`
	Error     string `json:"error"`
}

type CredentialsConfig struct {
	Login      string
	Password   string
	SessionKey string
}

func init() {
	balanceGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: "balance",
			Name:      "mcs",
			Help:      "Balance in mcs account",
		},
		[]string{"project"},
	)

	prometheus.MustRegister(balanceGauge)

	flag.Parse()
}

func main() {
	log.Println("Starting MCS balance exporter", version.Info())
	log.Println("Build context", version.BuildContext())

	if err := readConfig(); err != nil {
		log.Fatalln("Configuration error:", err.Error())
	}
	ctx = newContext(&credentials)

	if err := ctx.authorize(); err != nil {
		log.Fatalln(err.Error())
	}

	go startBalanceUpdater()

	srv := &http.Server{
		Addr:         *addr,
		WriteTimeout: time.Second * 2,
		ReadTimeout:  time.Second * 2,
		IdleTimeout:  time.Second * 60,

		Handler: nil,
	}

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "static/index.html")
	})

	go func() {
		log.Fatal(srv.ListenAndServe())
	}()

	log.Printf("MCS balance exporter has been started at address %s\n", *addr)
	log.Printf("Exporter will update balance every %d seconds\n", *interval)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Notify(c, syscall.SIGTERM)

	<-c

	log.Println("MCS balance exporter shutdown")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	err := srv.Shutdown(ctx)
	if err != nil {
		log.Fatal(err)
	}

	os.Exit(0)
}

func readConfig() error {
	if login, ok := os.LookupEnv("MCS_LOGIN"); ok {
		credentials.Login = login
	} else {
		return errors.New("environment \"MCS_LOGIN\" is not set")
	}

	if password, ok := os.LookupEnv("MCS_PASSWORD"); ok {
		credentials.Password = password
	} else {
		return errors.New("environment \"MCS_PASSWORD\" is not set")
	}

	return nil
}

func startBalanceUpdater() {
	for {

		if err := loadBalance(); err != nil {
			log.Println(err.Error())
			hasError = true
			retryCount++
			if retryCount >= *retryLimit {
				log.Printf("Retry limit %d has been exceeded\n", *retryLimit)
				hasError = false
				retryCount = 0
			}
		} else {
			hasError = false
			retryCount = 0
		}

		if hasError {
			log.Printf("Request will retry after %d seconds\n", *retryInterval)
			time.Sleep(time.Second * time.Duration(*retryInterval))
		} else {
			time.Sleep(time.Second * time.Duration(*interval))
		}
	}
}

func hideCredentials(format string, args ...interface{}) string {
	var message = fmt.Sprintf(format, args...)
	message = strings.Replace(message, credentials.Login, "<mcs-login>", -1)
	message = strings.Replace(message, credentials.Password, "<mcs-password>", -1)

	return message
}

func newError(format string, args ...interface{}) error {
	return errors.New(hideCredentials(format, args...))
}

func loadBalance() error {

	log.Printf("there are %v projects %v", len(ctx.projectsIDs), ctx.projectsIDs)
	for _, project := range ctx.projectsIDs {

		var balance, err = ctx.getBalance(project.id)
		if err != nil {
			return err
		}
		balanceGauge.With(prometheus.Labels{"project": project.title}).Set(balance)
	}

	return nil
}
