package commands

import "github.com/goravel/framework/contracts/console"

func printReport(ctx console.Context, info, warning, line, soft []string) {
	for _, text := range warning {
		ctx.Warning(redactedLine(text))
	}
	for _, text := range info {
		ctx.Info(text)
	}
	for _, text := range line {
		ctx.Line(redactedLine(text))
	}
	for _, text := range soft {
		ctx.Error(redactedLine(text))
	}
}
