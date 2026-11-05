package middleware

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	contractshttp "github.com/goravel/framework/contracts/http"
	goravelgin "github.com/goravel/gin"

	"github.com/macrowallets/waas/packages/rampengine/app/container"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	requestlogflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/requestlog"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/requestlog"
)

const maxLoggedBodyBytes = 64 * 1024

// RequestLogger captures HTTP requests and responses for the admin explorer.
func RequestLogger() contractshttp.Middleware {
	features := facades.Feature()
	writer := container.MustMake[*requestlog.Writer](container.RequestLogWriterKey)
	return func(ctx contractshttp.Context) {
		path := ctx.Request().Path()
		if shouldSkipRequestLog(path) {
			ctx.Request().Next()
			return
		}
		audience := audienceForPath(path)
		if audience == "" {
			ctx.Request().Next()
			return
		}

		enabled, err := features.IsActive(ctx.Context(), requestlogflags.Enabled, nil)
		if err != nil || !enabled {
			ctx.Request().Next()
			return
		}

		req := ctx.Request().Origin()
		var rawBody []byte
		if req != nil {
			rawBody, _ = ReadAndRestoreBody(req)
		}

		// The column is a uuid, and a spelling Postgres refuses would fail the
		// INSERT of every row flushed with this one.
		requestID, err := uuid.Parse(ctx.Request().Header("X-Request-ID", ""))
		if err != nil {
			requestID = uuid.New()
			ctx.Response().Header("X-Request-ID", requestID.String())
		}

		started := time.Now()
		ctx.Request().Next()
		duration := time.Since(started)

		redactor := requestlog.Redactor{}
		reqHeaders := redactor.Headers(ctx.Request().Headers())
		reqHeadersJSON, _ := json.Marshal(reqHeaders)

		contentType := ""
		if req != nil {
			contentType = req.Header.Get("Content-Type")
		}
		reqBody := requestlog.TruncateBody(redactor.Body(contentType, rawBody), maxLoggedBodyBytes)

		status, respHeaders, respBodyBytes := responseCapture(ctx)
		respHeadersJSON, _ := json.Marshal(redactor.Headers(respHeaders))
		respCT := headerGet(respHeaders, "Content-Type")
		respBody := requestlog.TruncateBody(redactor.Body(respCT, respBodyBytes), maxLoggedBodyBytes)

		queryJSON, _ := json.Marshal(redactor.Query(ctx.Request().Queries()))

		var userID *uint
		if token, ok := acceptedToken(ctx); ok {
			if n, ok := requests.ParseID(token.Key); ok {
				userID = &n
			}
		}
		var adminID *uint
		if admin := CurrentAdmin(ctx); admin != nil {
			adminID = &admin.ID
		}

		clientIP := ctx.Request().Ip()
		row := models.RequestLog{
			RequestID:       requestID.String(),
			Audience:        audience,
			Method:          ctx.Request().Method(),
			Path:            redactor.Path(path, ctx.Request().OriginPath()),
			Query:           string(queryJSON),
			Status:          status,
			DurationMs:      int(duration.Milliseconds()),
			RequestHeaders:  string(reqHeadersJSON),
			ResponseHeaders: string(respHeadersJSON),
			RequestBody:     reqBody,
			ResponseBody:    respBody,
			CreatedAt:       started.UTC(),
		}
		if clientIP != "" {
			row.ClientIP = &clientIP
		}
		row.UserID = userID
		row.AdminID = adminID
		if customer := CurrentCustomer(ctx); customer != nil {
			row.CustomerID = &customer.ID
		}

		writer.Enqueue(row)
	}
}

// shouldSkipRequestLog leaves out the event stream, a response that never ends,
// and the explorer, whose responses are logged bodies: logging them copies them.
func shouldSkipRequestLog(path string) bool {
	return path == "/api/events" || strings.HasPrefix(path, "/admin/api/request-logs")
}

func audienceForPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/"):
		return models.RequestLogAudienceClient
	case strings.HasPrefix(path, "/admin/api/"):
		return models.RequestLogAudienceAdmin
	case strings.HasPrefix(path, "/webhooks/"):
		return models.RequestLogAudienceWebhook
	default:
		return ""
	}
}

func responseCapture(ctx contractshttp.Context) (int, map[string][]string, []byte) {
	status := contractshttp.StatusOK
	var headers map[string][]string
	var body []byte

	if origin := ctx.Response().Origin(); origin != nil {
		if origin.Body() != nil {
			body = append([]byte(nil), origin.Body().Bytes()...)
		}
		if h := origin.Header(); h != nil {
			headers = h
		}
	}

	if gc, ok := ctx.(*goravelgin.Context); ok {
		status = gc.Instance().Writer.Status()
		if len(body) == 0 {
			if bw, ok := gc.Instance().Writer.(*goravelgin.BodyWriter); ok && bw.Body() != nil {
				body = append([]byte(nil), bw.Body().Bytes()...)
			}
		}
	}
	return status, headers, body
}

func headerGet(headers map[string][]string, key string) string {
	if headers == nil {
		return ""
	}
	for k, vals := range headers {
		if strings.EqualFold(k, key) && len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}
