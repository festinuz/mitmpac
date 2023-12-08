package middlewares

import (
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/urfave/negroni"
)

func getHostname() string {
	name, err := os.Hostname()
	if err != nil {
		panic(err)
	}
	return name
}

var requestsProcessed = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "The total number of processed requests",
		ConstLabels: prometheus.Labels{
			"hostname": getHostname(),
		},
	},
	[]string{"method", "handler", "code"},
)

var requestsTimings = promauto.NewSummaryVec(
	prometheus.SummaryOpts{
		Name:       "http_requests_duration_seconds",
		Help:       "The timings of all requests",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
		ConstLabels: prometheus.Labels{
			"hostname": getHostname(),
		},
	},
	[]string{"method", "handler"},
)

var ActiveConfigs = promauto.NewGauge(
	prometheus.GaugeOpts{
		Name: "mitmpac_active_configs",
		Help: "Active mitmpac configs served by the server",
		ConstLabels: prometheus.Labels{
			"hostname": getHostname(),
		},
	},
)

func setDefaultRouteMetrics(method string, path string) {
	requestsProcessed.WithLabelValues(method, path, "200").Add(0)
	requestsProcessed.WithLabelValues(method, path, "500").Add(0)
	requestsTimings.WithLabelValues(method, path).Observe(0)
}

func SetDefaultRoutesMetrics(routes []chi.Route) {
	for _, route := range routes {
		for method := range route.Handlers {
			setDefaultRouteMetrics(method, route.Pattern)
		}
	}
}

func MetricsMiddleware(wrappedHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		routeContext := chi.RouteContext(request.Context())
		method := request.Method
		url := routeContext.RoutePattern()

		start := time.Now()

		wrappedWriter := negroni.NewResponseWriter(writer)
		wrappedHandler.ServeHTTP(wrappedWriter, request)

		status := strconv.Itoa(wrappedWriter.Status())
		elapsed := float64(time.Since(start)) / float64(time.Second)
		requestsProcessed.WithLabelValues(method, url, status).Inc()
		requestsTimings.WithLabelValues(method, url).Observe(elapsed)
	})
}
