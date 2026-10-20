package architecture

import (
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestWebhook_DeadLetterQueue_RaisesAnAlarmInEveryTemplate requires, in each infra
// template, a CloudWatch alarm on the dead-letter queue the webhook queue redrives
// to, raised as soon as one message is visible there: a delivery that exhausted its
// receives is otherwise only seen by someone who runs make dlq-check.
func TestWebhook_DeadLetterQueue_RaisesAnAlarmInEveryTemplate(t *testing.T) {
	root := os.DirFS(sharedModule(t).Root)
	for _, name := range []string{"template.yaml", "template.dev.yaml"} {
		t.Run(name, func(t *testing.T) {
			raw, err := fs.ReadFile(root, name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			resources := templateResources(string(raw))
			dlq := redriveTarget(resources["WebhookQueue"])
			if dlq == "" || resources[dlq] == "" {
				t.Fatalf("the webhook queue has no dead-letter queue resource (redrive target %q)", dlq)
			}
			alarms := alarmsOnQueue(resources, dlq)
			if len(alarms) != 1 {
				t.Fatalf("alarms on %s: %v, want exactly one", dlq, alarms)
			}
			alarm := resources[alarms[0]]
			for _, want := range []string{
				"Namespace: AWS/SQS",
				"MetricName: ApproximateNumberOfMessagesVisible",
				"ComparisonOperator: GreaterThanThreshold",
				"Threshold: 0",
				// A DLQ that holds messages nobody touches goes inactive after six hours and
				// stops reporting: the alarm must stay raised, not fall back to OK.
				"TreatMissingData: ignore",
				"AlarmActions: !If [HasAlarmTopic, [!Ref AlarmTopicArn], !Ref AWS::NoValue]",
			} {
				if !strings.Contains(alarm, want) {
					t.Errorf("%s lacks %q", alarms[0], want)
				}
			}
			if !strings.Contains(string(raw), `HasAlarmTopic: !Not [!Equals [!Ref AlarmTopicArn, ""]]`) {
				t.Error("the template has no HasAlarmTopic condition on the AlarmTopicArn parameter")
			}
		})
	}
}

// templateResources returns the text of each top-level resource under
// Resources:, by logical id. CloudFormation intrinsics stay as text.
func templateResources(template string) map[string]string {
	resources := map[string]string{}
	inResources := false
	current := ""
	for _, line := range strings.Split(template, "\n") {
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if indent == 0 {
			inResources = trimmed == "Resources:"
			current = ""
			continue
		}
		if !inResources {
			continue
		}
		if indent == 2 && strings.HasSuffix(trimmed, ":") && !strings.Contains(trimmed, " ") {
			current = strings.TrimSuffix(trimmed, ":")
			resources[current] = ""
			continue
		}
		if current != "" {
			resources[current] += trimmed + "\n"
		}
	}
	return resources
}

var (
	redriveTargetPattern = regexp.MustCompile(`deadLetterTargetArn: !GetAtt (\w+)\.Arn`)
	alarmQueuePattern    = regexp.MustCompile(`Value: !GetAtt (\w+)\.QueueName`)
)

// redriveTarget is the logical id of the queue a resource's RedrivePolicy names.
func redriveTarget(resource string) string {
	match := redriveTargetPattern.FindStringSubmatch(resource)
	if match == nil {
		return ""
	}
	return match[1]
}

// alarmsOnQueue lists the CloudWatch alarms whose dimension names queue.
func alarmsOnQueue(resources map[string]string, queue string) []string {
	ids := make([]string, 0, len(resources))
	for id := range resources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var alarms []string
	for _, id := range ids {
		body := resources[id]
		if !strings.Contains(body, "Type: AWS::CloudWatch::Alarm") {
			continue
		}
		if match := alarmQueuePattern.FindStringSubmatch(body); match != nil && match[1] == queue {
			alarms = append(alarms, id)
		}
	}
	return alarms
}

// TestConfirmation_Tracker_ReadsTheScanAlertThresholds requires the confirmation
// tracker function, which raises the deposit scan alerts, to get their thresholds.
func TestConfirmation_Tracker_ReadsTheScanAlertThresholds(t *testing.T) {
	raw, err := fs.ReadFile(os.DirFS(sharedModule(t).Root), "template.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tracker := templateResources(string(raw))["ConfirmationTrackerFunction"]
	for _, want := range []string{
		"DEPOSIT_SCAN_ALERT_LAG_BLOCKS: !Ref DepositScanAlertLagBlocks",
		"DEPOSIT_SCAN_ALERT_STALE_MINUTES: !Ref DepositScanAlertStaleMinutes",
	} {
		if !strings.Contains(tracker, want) {
			t.Errorf("ConfirmationTrackerFunction lacks %q", want)
		}
	}
}
