package analyze

import (
	"strings"
	"testing"
)

func TestAnonymizeRoundTrip(t *testing.T) {
	in := `JOB "orders-enrichment" failed on 10.0.0.7:34567 writing to s3://prod-bucket/ckpt-320`
	anon, mapping := Anonymize(in, []string{"orders-enrichment"})

	for _, secret := range []string{"orders-enrichment", "10.0.0.7:34567", "s3://prod-bucket/ckpt-320"} {
		if strings.Contains(anon, secret) {
			t.Fatalf("anonymized text still contains %q: %s", secret, anon)
		}
	}
	if len(mapping) != 3 {
		t.Fatalf("want 3 mappings, got %d: %v", len(mapping), mapping)
	}
	if got := Deanonymize(anon, mapping); got != in {
		t.Fatalf("round trip failed:\n in: %s\nout: %s", in, got)
	}
}

func TestAnonymizeStablePlaceholders(t *testing.T) {
	in := "host 10.0.0.7 then 10.0.0.7 again"
	anon, mapping := Anonymize(in, nil)
	if len(mapping) != 1 {
		t.Fatalf("same value must map once, got %v", mapping)
	}
	if strings.Count(anon, "<host-1>") != 2 {
		t.Fatalf("placeholder not reused: %s", anon)
	}
}

func TestScrubberCredentialsAndHosts(t *testing.T) {
	sc := NewScrubber(nil)
	in := `Caused by: java.sql.SQLException: Cannot connect to jdbc:postgresql://dbuser:P@ssw0rd@pg-primary-1.prod.internal:5432/orders
	at org.apache.flink.runtime.jobmaster.JobMaster.start(JobMaster.java:123)
	sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username="svc" password="hunter2";
	Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc
	bootstrap: kafka-broker-3.prod.corp:9092  key AKIAIOSFODNN7EXAMPLE`
	out := sc.Scrub(in)
	for _, leak := range []string{"P@ssw0rd", "hunter2", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9", "AKIAIOSFODNN7EXAMPLE", "pg-primary-1.prod.internal", "kafka-broker-3.prod.corp"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q in scrubbed output:\n%s", leak, out)
		}
	}
	for _, keep := range []string{"org.apache.flink.runtime.jobmaster.JobMaster", "JobMaster.java:123", "org.apache.kafka.common.security.plain.PlainLoginModule"} {
		if !strings.Contains(out, keep) {
			t.Errorf("over-scrubbed %q — diagnostic signal lost:\n%s", keep, out)
		}
	}
	// hosts must be restorable placeholders
	restored := sc.Restore(out)
	if !strings.Contains(restored, "pg-primary-1.prod.internal") {
		t.Error("host placeholder did not restore")
	}
}
