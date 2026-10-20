package architecture

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// cloudFormationTemplates are the templates `make validate` and `make deploy` read.
var cloudFormationTemplates = []string{"template.yaml", "template.dev.yaml"}

// TestTemplate_LambdaModes_AreHandledByMain refuses a LAMBDA_MODE in template.yaml
// that main.go does not switch on. An unknown mode falls to runLocal, which inside
// Lambda never hands the runtime a handler (every invocation times out) and starts
// the local workers there, a second scanner and outbox consumer nobody sees.
func TestTemplate_LambdaModes_AreHandledByMain(t *testing.T) {
	module := sharedModule(t)
	modes := templateLambdaModes(readTemplate(t, module.Root, "template.yaml"))
	if len(modes) == 0 {
		t.Fatal("template.yaml sets no LAMBDA_MODE")
	}
	handled := mainLambdaModes(mainSource(t, module).AST)
	if len(handled) == 0 {
		t.Fatal("main.go switches on no LAMBDA_MODE")
	}
	for _, problem := range unhandledLambdaModes(modes, handled) {
		t.Errorf("template.yaml: %s", problem)
	}
}

func TestLambda_Modes_ReportsAModeMainDoesNotHandle(t *testing.T) {
	const template = `
Globals:
  Function:
    Timeout: 30
Resources:
  WebhookWorkerFunction:
    Type: AWS::Serverless::Function
    Properties:
      Environment:
        Variables:
          LAMBDA_MODE: webhook_worker
  WithdrawalWorkerFunction:
    Type: AWS::Serverless::Function
    Properties:
      Environment:
        Variables:
          LAMBDA_MODE: "withdrawal_worker"
`
	const src = `package main
func main() {
	switch mode {
	case "webhook_worker":
		lambda.Start(handleWebhookWorker)
	case "webhook_poller":
		runWebhookPoller()
	default:
		runLocal()
	}
}
`
	modes := templateLambdaModes([]byte(template))
	if len(modes) != 2 {
		t.Fatalf("modes = %v, want both functions", modes)
	}
	got := unhandledLambdaModes(modes, mainLambdaModes(parseSnippet(t, "main.go", src)))
	want := `WithdrawalWorkerFunction sets LAMBDA_MODE "withdrawal_worker", which main.go does not handle`
	if len(got) != 1 || got[0] != want {
		t.Fatalf("problems = %v, want [%s]", got, want)
	}
}

// TestTemplate_Schedules_AreValidEventBridgeExpressions refuses a schedule that
// EventBridge rejects at deploy time, such as rate(5 seconds): plain `sam validate`
// accepts it and the stack fails to create.
func TestTemplate_Schedules_AreValidEventBridgeExpressions(t *testing.T) {
	module := sharedModule(t)
	for _, name := range cloudFormationTemplates {
		for _, schedule := range templateSchedules(readTemplate(t, module.Root, name)) {
			if err := validScheduleExpression(schedule.expression); err != nil {
				t.Errorf("%s line %d: %v", name, schedule.line, err)
			}
		}
	}
}

func TestSchedule_Expression_FollowsTheEventBridgeRules(t *testing.T) {
	valid := []string{
		"rate(1 minute)", "rate(5 minutes)", "rate(1 hour)", "rate(12 hours)", "rate(1 day)", "rate(7 days)",
		"cron(0 12 * * ? *)", "cron(0/5 * * * ? *)",
	}
	for _, expression := range valid {
		if err := validScheduleExpression(expression); err != nil {
			t.Errorf("%s refused: %v", expression, err)
		}
	}
	invalid := []string{
		"rate(5 seconds)", "rate(15 seconds)", "rate(1 second)", "rate(0 minutes)", "rate(1 minutes)",
		"rate(2 minute)", "rate(minute)", "cron(0 12 * * ?)", "every 5 seconds", "",
	}
	for _, expression := range invalid {
		if err := validScheduleExpression(expression); err == nil {
			t.Errorf("%q accepted", expression)
		}
	}
	const template = `
Resources:
  ConfirmationTrackerFunction:
    Type: AWS::Serverless::Function
    Properties:
      Events:
        Schedule:
          Type: Schedule
          Properties:
            Schedule: rate(1 minute)
  Rule:
    Type: AWS::Events::Rule
    Properties:
      ScheduleExpression: "rate(5 seconds)"
`
	schedules := templateSchedules([]byte(template))
	if len(schedules) != 2 || schedules[0].expression != "rate(1 minute)" || schedules[1].expression != "rate(5 seconds)" {
		t.Fatalf("schedules = %v, want the SAM event and the rule, and not the event's logical id", schedules)
	}
}

// templateLambdaMode is one LAMBDA_MODE a template resource sets.
type templateLambdaMode struct {
	function string
	mode     string
}

// templateSchedule is one schedule expression of a template, with its line.
type templateSchedule struct {
	line       int
	expression string
}

func readTemplate(t *testing.T, root, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Clean(filepath.Join(root, name)))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return content
}

func mainSource(t *testing.T, module *Module) *SourceFile {
	t.Helper()
	for _, file := range module.ProductionFiles() {
		if file.Path == "main.go" {
			return file
		}
	}
	t.Fatal("main.go was not parsed")
	return nil
}

// templateLambdaModes reads every LAMBDA_MODE the resources of a template set,
// keyed by the resource logical id (a key at indent 2). Lines are read as text, as
// parseSQSConsumers does, so the CloudFormation intrinsics need no YAML tags.
func templateLambdaModes(template []byte) []templateLambdaMode {
	var found []templateLambdaMode
	resource := ""
	for _, line := range strings.Split(string(template), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case indent == 0:
			resource = ""
		case indent == 2 && strings.HasSuffix(trimmed, ":") && !strings.Contains(trimmed, " "):
			resource = strings.TrimSuffix(trimmed, ":")
		case resource != "" && strings.HasPrefix(trimmed, "LAMBDA_MODE:"):
			mode := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "LAMBDA_MODE:")), `"'`)
			found = append(found, templateLambdaMode{function: resource, mode: mode})
		}
	}
	return found
}

// templateSchedules reads the value of every Schedule (SAM Schedule event) and
// ScheduleExpression (AWS::Events::Rule, AWS::Scheduler::Schedule) key. A key
// without a value is a mapping, such as an event whose logical id is Schedule.
func templateSchedules(template []byte) []templateSchedule {
	var found []templateSchedule
	for number, line := range strings.Split(string(template), "\n") {
		trimmed := strings.TrimSpace(line)
		for _, key := range []string{"Schedule:", "ScheduleExpression:"} {
			if !strings.HasPrefix(trimmed, key) {
				continue
			}
			value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, key)), `"'`)
			if value != "" {
				found = append(found, templateSchedule{line: number + 1, expression: value})
			}
		}
	}
	return found
}

// mainLambdaModes returns the string cases of the switches in main.go's main.
func mainLambdaModes(file *ast.File) map[string]bool {
	modes := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "main" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				literal, ok := expr.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				if mode, err := strconv.Unquote(literal.Value); err == nil {
					modes[mode] = true
				}
			}
			return true
		})
	}
	return modes
}

func unhandledLambdaModes(modes []templateLambdaMode, handled map[string]bool) []string {
	var problems []string
	for _, item := range modes {
		if !handled[item.mode] {
			problems = append(problems, fmt.Sprintf("%s sets LAMBDA_MODE %q, which main.go does not handle", item.function, item.mode))
		}
	}
	return problems
}

var (
	rateExpression = regexp.MustCompile(`^rate\(([0-9]+) ([a-z]+)\)$`)
	cronExpression = regexp.MustCompile(`^cron\((.+)\)$`)
)

// validScheduleExpression applies the EventBridge rules: a rate is a positive whole
// number of minutes, hours or days (singular unit for 1, plural above), so nothing
// runs more often than once a minute; a cron has six fields.
func validScheduleExpression(expression string) error {
	if match := rateExpression.FindStringSubmatch(expression); match != nil {
		value, err := strconv.Atoi(match[1])
		if err != nil || value < 1 {
			return fmt.Errorf("%s: the rate must be a positive whole number", expression)
		}
		unit := match[2]
		singular := unit == "minute" || unit == "hour" || unit == "day"
		plural := unit == "minutes" || unit == "hours" || unit == "days"
		switch {
		case !singular && !plural:
			return fmt.Errorf("%s: EventBridge rates are in minutes, hours or days (at least 1 minute)", expression)
		case value == 1 && !singular:
			return fmt.Errorf("%s: a rate of 1 takes a singular unit", expression)
		case value > 1 && !plural:
			return fmt.Errorf("%s: a rate above 1 takes a plural unit", expression)
		}
		return nil
	}
	if match := cronExpression.FindStringSubmatch(expression); match != nil {
		if fields := strings.Fields(match[1]); len(fields) != 6 {
			return fmt.Errorf("%s: an EventBridge cron has 6 fields, not %d", expression, len(fields))
		}
		return nil
	}
	return fmt.Errorf("%q is neither rate(...) nor cron(...)", expression)
}
