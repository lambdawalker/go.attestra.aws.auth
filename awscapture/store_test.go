package awscapture

import (
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/lambdawalker/go.attestra.aws.auth/capture"
	"testing"
)

func TestRecordRoundTripPreservesIDAndPendingWork(t *testing.T) {
	in := capture.Record{ID: "abcdef", Owner: "owner", Revision: 3, Work: "cleanup", Due: 123, TTL: 999}
	m, e := recordItem(in)
	if e != nil {
		t.Fatal(e)
	}
	var got capture.Record
	if e = attributevalue.UnmarshalMap(m, &got); e != nil {
		t.Fatal(e)
	}
	if got.ID != in.ID || got.Owner != in.Owner {
		t.Fatal("DynamoDB key overwrote capture ID", got)
	}
	if _, ok := m["ttl"]; ok {
		t.Fatal("pending cleanup must never expire through TTL")
	}
}
