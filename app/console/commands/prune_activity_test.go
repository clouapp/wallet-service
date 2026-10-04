package commands

import "testing"

func TestActivityRetentionDays(t *testing.T) {
	days, err := activityRetentionDays("")
	if err != nil || days != defaultActivityRetentionDays {
		t.Fatalf("default = %d, %v", days, err)
	}
	days, err = activityRetentionDays("30")
	if err != nil || days != 30 {
		t.Fatalf("flag = %d, %v", days, err)
	}
	if _, err := activityRetentionDays("0"); err == nil {
		t.Fatal("zero days was accepted")
	}
	if _, err := activityRetentionDays("-4"); err == nil {
		t.Fatal("negative days was accepted")
	}
	if _, err := activityRetentionDays("soon"); err == nil {
		t.Fatal("text was accepted")
	}
}
