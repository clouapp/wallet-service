package middleware

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	cmsfacades "github.com/macrowallets/waas/packages/rampengine/packages/cms/facades"
)

// Localize makes the CMS locale Accept-Language asks for the request's own,
// so a handler reads it with facades.App().CurrentLocale(ctx). A header that
// matches no configured locale — or none at all — leaves app.locale, which
// the CMS read chain then falls back from.
//
// The response names the locale it targets and varies by the header, so no
// cache in front of it serves one reader's language to another.
func Localize() contractshttp.Middleware {
	locales := cmsfacades.CMS().Locales()
	return func(ctx contractshttp.Context) {
		if locale, ok := locales.Match(ctx.Request().Header("Accept-Language", "")); ok {
			facades.App().SetLocale(ctx, locale)
		}
		header := ctx.Response().Writer().Header()
		header.Add("Vary", "Accept-Language")
		header.Set("Content-Language", facades.App().CurrentLocale(ctx))
		ctx.Request().Next()
	}
}
