package reconcile

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRunUsesDeclaredOrder(t *testing.T) {
	var calls []string
	services := []Service{
		fakeService("lidarr", &calls, nil),
		fakeService("sftpgo", &calls, nil),
		fakeService("navidrome", &calls, nil),
	}
	outcomes, err := Run(context.Background(), services)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"lidarr", "sftpgo", "navidrome"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
	if len(outcomes) != 3 {
		t.Fatalf("outcomes=%v", outcomes)
	}
}

func TestServicesUseSafeDependencyOrder(t *testing.T) {
	services := Services(Options{})
	names := make([]string, 0, len(services))
	for _, service := range services {
		names = append(names, service.Name)
	}
	want := []string{"lidarr", "sftpgo", "navidrome"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("services=%v want=%v", names, want)
	}
}

func TestRunStopsAndWrapsFirstError(t *testing.T) {
	var calls []string
	services := []Service{
		fakeService("lidarr", &calls, nil),
		fakeService("sftpgo", &calls, errors.New("API unavailable")),
		fakeService("navidrome", &calls, nil),
	}
	outcomes, err := Run(context.Background(), services)
	if err == nil || !strings.Contains(err.Error(), "reconcile sftpgo: API unavailable") {
		t.Fatalf("outcomes=%v err=%v", outcomes, err)
	}
	if want := []string{"lidarr", "sftpgo"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
	if len(outcomes) != 1 {
		t.Fatalf("outcomes=%v", outcomes)
	}
}

func fakeService(name string, calls *[]string, applyErr error) Service {
	return Service{
		Name: name,
		Apply: func(context.Context) (Outcome, error) {
			*calls = append(*calls, name)
			return Outcome{Service: name, Changed: true, Message: "applied"}, applyErr
		},
	}
}
