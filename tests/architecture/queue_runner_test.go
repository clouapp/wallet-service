package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestOneConsumerPerQueue refuses a second worker for one queue, and a
// goroutine loop that drains what that queue's deployed worker drains
// (.ai/guidelines/queues-and-workers.md). The local outbox stand-in is the
// consumer only when no webhook queue is configured.
func TestOne_Consumer_PerQueue(t *testing.T) {
	module := sharedModule(t)
	template, err := os.ReadFile(filepath.Join(module.Root, "template.yaml"))
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	found, err := parseSQSConsumers(template)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	consumers := consumersByQueue(found)
	if len(consumers) == 0 {
		t.Fatal("template has no SQS consumer")
	}

	var violations []string
	for _, queue := range sortedKeys(consumers) {
		functions := consumers[queue]
		if len(functions) > 1 {
			violations = append(violations, fmt.Sprintf("queue %s has workers %s", queue, strings.Join(functions, ", ")))
		}
	}

	var mainFile *SourceFile
	for _, file := range module.ProductionFiles() {
		if file.Path == "main.go" {
			mainFile = file
			break
		}
	}
	if mainFile == nil {
		t.Fatal("main.go was not parsed")
	}
	modes := sqsModes(found)
	allowed := map[string]bool{}
	for _, name := range deployedSQSHandlers(mainFile.AST, modes) {
		allowed[name] = true
	}
	if len(allowed) == 0 {
		t.Fatal("no deployed SQS handler in main.go")
	}
	if !webhookStandInIsExclusive(mainFile.AST) {
		violations = append(violations, "main.go must run the webhook outbox only when vault.queues.webhook is empty")
	}
	for _, handler := range deployedSQSHandlers(mainFile.AST, modes) {
		if goCalls(mainFile.AST, handler) > 0 {
			violations = append(violations, fmt.Sprintf("main.go starts %s in a goroutine beside the deployed worker", handler))
		}
	}
	for _, file := range module.ProductionFiles() {
		for _, name := range sqsRecordDrainers(file.AST) {
			if !allowed[name] {
				violations = append(violations, fmt.Sprintf("%s drains an SQS queue from %s", file.Path, name))
			}
		}
		for _, hit := range unguardedQueueDrains(file.AST, drainsWebhookDeliver(file.Path)) {
			violations = append(violations, fmt.Sprintf("%s goroutine loop drains %s", file.Path, hit))
		}
	}
	for _, line := range violations {
		t.Errorf("second consumer: %s", line)
	}
}

func TestSQS_Consumers_RefusesASecondFunctionOnTheSameQueue(t *testing.T) {
	const template = `
Resources:
  WebhookWorkerFunction:
    Type: AWS::Serverless::Function
    Properties:
      Environment:
        Variables:
          LAMBDA_MODE: webhook_worker
      Events:
        SQSEvent:
          Type: SQS
          Properties:
            Queue: !GetAtt WebhookQueue.Arn
  ExtraWebhookFunction:
    Type: AWS::Serverless::Function
    Properties:
      Environment:
        Variables:
          LAMBDA_MODE: webhook_worker
      Events:
        Again:
          Type: SQS
          Properties:
            Queue: !GetAtt WebhookQueue.Arn
`
	consumers, err := sqsConsumers([]byte(template))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := consumers["WebhookQueue"]; len(got) != 2 {
		t.Fatalf("WebhookQueue consumers = %v, want both functions", got)
	}
}

func TestSQS_Consumers_KeepsOneFunctionPerQueue(t *testing.T) {
	const template = `
Resources:
  WebhookWorkerFunction:
    Type: AWS::Serverless::Function
    Properties:
      Environment:
        Variables:
          LAMBDA_MODE: webhook_worker
      Events:
        SQSEvent:
          Type: SQS
          Properties:
            Queue: !GetAtt WebhookQueue.Arn
  WithdrawalWorkerFunction:
    Type: AWS::Serverless::Function
    Properties:
      Environment:
        Variables:
          LAMBDA_MODE: withdrawal_worker
      Events:
        SQSEvent:
          Type: SQS
          Properties:
            Queue: !Ref WithdrawalQueue
`
	consumers, err := sqsConsumers([]byte(template))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := consumers["WebhookQueue"]; len(got) != 1 || got[0] != "WebhookWorkerFunction" {
		t.Fatalf("WebhookQueue consumers = %v", got)
	}
	if got := consumers["WithdrawalQueue"]; len(got) != 1 || got[0] != "WithdrawalWorkerFunction" {
		t.Fatalf("WithdrawalQueue consumers = %v", got)
	}
}

func TestUnguarded_Goroutine_DrainIsRefused(t *testing.T) {
	const src = `package main
func start() {
	go func() {
		for {
			webhooks.Deliver(ctx, msg)
		}
	}()
	loops.every(ctx, interval, func() {
		deliverer.DeliverPending(ctx, 25)
	})
}
`
	file := parseSnippet(t, "main.go", src)
	got := unguardedQueueDrains(file, true)
	sort.Strings(got)
	want := []string{"DeliverPending", "webhooks.Deliver"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("drains = %v, want %v", got, want)
	}
}

func TestOutbox_Stand_InBehindDeliverOutboxIsTheOnlyConsumer(t *testing.T) {
	const src = `package localworkers
func start(cfg Config) {
	if cfg.DeliverOutbox {
		loops.every(ctx, interval, func() {
			deliverer.DeliverPending(ctx, 25)
		})
	}
}
`
	file := parseSnippet(t, "app/services/localworkers/runner.go", src)
	if got := unguardedQueueDrains(file, true); len(got) != 0 {
		t.Fatalf("guarded stand-in reported %v", got)
	}
}

func TestDeployed_Handler_IsNotAGoroutineLoop(t *testing.T) {
	const src = `package main
func handleWebhookWorker() {
	for _, record := range sqsEvent.Records {
		webhooks.Deliver(ctx, msg)
	}
}
`
	file := parseSnippet(t, "main.go", src)
	if got := unguardedQueueDrains(file, true); len(got) != 0 {
		t.Fatalf("deployed handler reported %v", got)
	}
	if got := sqsRecordDrainers(file); len(got) != 1 || got[0] != "handleWebhookWorker" {
		t.Fatalf("record drainers = %v", got)
	}
}

// sqsConsumer is one deployed function bound to one queue.
type sqsConsumer struct {
	function string
	mode     string
	queue    string
}

// sqsConsumers maps a queue logical id to the functions whose SQS event
// sources read it. A queue with two functions has two workers.
func sqsConsumers(template []byte) (map[string][]string, error) {
	found, err := parseSQSConsumers(template)
	if err != nil {
		return nil, err
	}
	return consumersByQueue(found), nil
}

func consumersByQueue(found []sqsConsumer) map[string][]string {
	consumers := map[string][]string{}
	for _, item := range found {
		consumers[item.queue] = append(consumers[item.queue], item.function)
	}
	for queue := range consumers {
		sort.Strings(consumers[queue])
	}
	return consumers
}

func sqsModes(found []sqsConsumer) map[string]bool {
	modes := map[string]bool{}
	for _, item := range found {
		if item.mode != "" {
			modes[item.mode] = true
		}
	}
	return modes
}

// parseSQSConsumers reads SAM functions that subscribe to an SQS queue.
// It keeps CloudFormation intrinsics (!GetAtt, !Ref) as text.
func parseSQSConsumers(template []byte) ([]sqsConsumer, error) {
	var found []sqsConsumer
	var function, mode string
	isFunction := false
	inEvents := false
	eventIsSQS := false
	sawFunction := false

	for _, line := range strings.Split(string(template), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 {
			function = ""
			isFunction = false
			inEvents = false
			eventIsSQS = false
			continue
		}
		if indent == 2 && strings.HasSuffix(trimmed, ":") && !strings.Contains(trimmed, " ") {
			function = strings.TrimSuffix(trimmed, ":")
			mode = ""
			isFunction = false
			inEvents = false
			eventIsSQS = false
			continue
		}
		if function == "" {
			continue
		}
		if trimmed == "Type: AWS::Serverless::Function" {
			isFunction = true
			sawFunction = true
			continue
		}
		if !isFunction {
			continue
		}
		if strings.HasPrefix(trimmed, "LAMBDA_MODE:") {
			mode = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "LAMBDA_MODE:")), `"'`)
			continue
		}
		if trimmed == "Events:" {
			inEvents = true
			eventIsSQS = false
			continue
		}
		if inEvents && strings.HasPrefix(trimmed, "Type:") {
			eventIsSQS = trimmed == "Type: SQS"
			continue
		}
		if !inEvents || !eventIsSQS || !strings.HasPrefix(trimmed, "Queue:") {
			continue
		}
		queue := queueLogicalID(strings.TrimSpace(strings.TrimPrefix(trimmed, "Queue:")))
		if queue == "" {
			return nil, fmt.Errorf("%s has an SQS event without a queue", function)
		}
		found = append(found, sqsConsumer{function: function, mode: mode, queue: queue})
		eventIsSQS = false
	}
	if !sawFunction {
		return nil, fmt.Errorf("template has no function")
	}
	return found, nil
}

// queueLogicalID reads !GetAtt WebhookQueue.Arn and !Ref WithdrawalQueue.
func queueLogicalID(raw string) string {
	raw = strings.Trim(raw, `"'`)
	fields := strings.Fields(raw)
	token := raw
	if len(fields) >= 2 && strings.HasPrefix(fields[0], "!") {
		token = fields[1]
	}
	name, _, _ := strings.Cut(token, ".")
	return name
}

// deployedSQSHandlers returns the functions main starts for SQS lambda modes.
func deployedSQSHandlers(file *ast.File, modes map[string]bool) []string {
	var names []string
	ast.Inspect(file, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok || len(clause.List) != 1 {
			return true
		}
		literal, ok := clause.List[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		mode, err := strconv.Unquote(literal.Value)
		if err != nil || !modes[mode] {
			return true
		}
		if name := lambdaStartTarget(clause.Body); name != "" {
			names = append(names, name)
		}
		return true
	})
	return names
}

func lambdaStartTarget(stmts []ast.Stmt) string {
	var name string
	for _, stmt := range stmts {
		ast.Inspect(stmt, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Start" {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "lambda" {
				return true
			}
			target, ok := call.Args[0].(*ast.Ident)
			if ok {
				name = target.Name
			}
			return true
		})
	}
	return name
}

func sqsRecordDrainers(file *ast.File) []string {
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name == nil {
			continue
		}
		if rangesOverRecords(fn.Body) {
			names = append(names, fn.Name.Name)
		}
	}
	return names
}

func rangesOverRecords(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(child ast.Node) bool {
		rng, ok := child.(*ast.RangeStmt)
		if !ok {
			return true
		}
		selector, ok := rng.X.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Records" {
			found = true
		}
		return true
	})
	return found
}

func goCalls(file *ast.File, name string) int {
	count := 0
	ast.Inspect(file, func(node ast.Node) bool {
		stmt, ok := node.(*ast.GoStmt)
		if !ok {
			return true
		}
		ident, ok := stmt.Call.Fun.(*ast.Ident)
		if ok && ident.Name == name {
			count++
		}
		return true
	})
	return count
}

// webhookStandInIsExclusive reports whether every DeliverOutbox value is the
// empty webhook queue URL, so the goroutine does not run beside the deployed worker.
func webhookStandInIsExclusive(file *ast.File) bool {
	found := false
	exclusive := true
	ast.Inspect(file, func(node ast.Node) bool {
		field, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok || key.Name != "DeliverOutbox" {
			return true
		}
		found = true
		if !isEmptyWebhookQueue(field.Value) {
			exclusive = false
		}
		return true
	})
	return found && exclusive
}

func isEmptyWebhookQueue(expr ast.Expr) bool {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok || binary.Op != token.EQL {
		return false
	}
	var call ast.Expr
	switch {
	case isEmptyString(binary.Y):
		call = binary.X
	case isEmptyString(binary.X):
		call = binary.Y
	default:
		return false
	}
	invocation, ok := call.(*ast.CallExpr)
	if !ok || len(invocation.Args) != 1 {
		return false
	}
	selector, ok := invocation.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "GetString" {
		return false
	}
	return isString(invocation.Args[0], "vault.queues.webhook")
}

func isEmptyString(expr ast.Expr) bool {
	return isString(expr, "")
}

func isString(expr ast.Expr, want string) bool {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	return err == nil && value == want
}

func drainsWebhookDeliver(path string) bool {
	return path == "main.go" ||
		strings.HasPrefix(path, "app/services/webhook/") ||
		strings.HasPrefix(path, "app/services/localworkers/")
}

// unguardedQueueDrains returns drain calls inside a goroutine loop that is
// not the outbox stand-in gated on DeliverOutbox.
func unguardedQueueDrains(file *ast.File, fileDrainsDeliver bool) []string {
	visitor := &drainVisitor{fileDrainsDeliver: fileDrainsDeliver}
	ast.Walk(visitor, file)
	return visitor.hits
}

type drainVisitor struct {
	inLoop            bool
	guarded           bool
	fileDrainsDeliver bool
	hits              []string
}

func (v *drainVisitor) Visit(node ast.Node) ast.Visitor {
	if node == nil {
		return nil
	}
	switch typed := node.(type) {
	case *ast.GoStmt:
		child := v.branch(launchedLoop(typed.Call), v.guarded)
		ast.Walk(child, typed.Call)
		v.hits = append(v.hits, child.hits...)
		return nil
	case *ast.IfStmt:
		if typed.Init != nil {
			ast.Walk(v, typed.Init)
		}
		if typed.Cond != nil {
			ast.Walk(v, typed.Cond)
		}
		child := v.branch(v.inLoop, v.guarded || namesDeliverOutbox(typed.Cond))
		ast.Walk(child, typed.Body)
		v.hits = append(v.hits, child.hits...)
		if typed.Else != nil {
			ast.Walk(v, typed.Else)
		}
		return nil
	case *ast.CallExpr:
		if isEveryCall(typed) {
			child := v.branch(true, v.guarded)
			for _, arg := range typed.Args {
				if _, ok := arg.(*ast.FuncLit); ok {
					ast.Walk(child, arg)
				}
			}
			v.hits = append(v.hits, child.hits...)
			for _, arg := range typed.Args {
				if _, ok := arg.(*ast.FuncLit); !ok {
					ast.Walk(v, arg)
				}
			}
			return nil
		}
		if v.inLoop && !v.guarded {
			if name, ok := queueDrainName(typed, v.fileDrainsDeliver); ok {
				v.hits = append(v.hits, name)
			}
		}
		return v
	case *ast.BasicLit:
		if v.inLoop && !v.guarded && typed.Kind == token.STRING {
			if value, err := strconv.Unquote(typed.Value); err == nil && strings.Contains(value, "queue:work") {
				v.hits = append(v.hits, "queue:work")
			}
		}
		return nil
	default:
		return v
	}
}

func (v *drainVisitor) branch(inLoop, guarded bool) *drainVisitor {
	return &drainVisitor{inLoop: inLoop, guarded: guarded, fileDrainsDeliver: v.fileDrainsDeliver}
}

func launchedLoop(call *ast.CallExpr) bool {
	if isEveryCall(call) {
		return true
	}
	literal, ok := call.Fun.(*ast.FuncLit)
	return ok && containsLoop(literal.Body)
}

func isEveryCall(call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name == "every"
	case *ast.SelectorExpr:
		return fun.Sel.Name == "every"
	default:
		return false
	}
}

func containsLoop(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(child ast.Node) bool {
		switch child.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			found = true
		}
		return true
	})
	return found
}

func namesDeliverOutbox(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "DeliverOutbox" {
			found = true
		}
		return true
	})
	return found
}

func queueDrainName(call *ast.CallExpr, fileDrainsDeliver bool) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch selector.Sel.Name {
	case "DeliverPending":
		return "DeliverPending", true
	case "Deliver":
		if ident, ok := selector.X.(*ast.Ident); ok && ident.Name == "webhooks" {
			return "webhooks.Deliver", true
		}
		if fileDrainsDeliver {
			return "Deliver", true
		}
	}
	return "", false
}

func parseSnippet(t *testing.T, path, src string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func sortedKeys(values map[string][]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
