package main_test

import (
	"testing"

	"github.com/kryspal/plugin-sdk/plugintest"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

func TestConformance(t *testing.T) {
	plugintest.RunSource(t, ".", plugintest.Options{
		Person:    &plugintest.PersonCase{ID: "p1"},
		RunTasks:  []string{"refresh_catalog"},
		Recognize: &plugintest.RecognizeCase{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Path: []string{"ABC-123 Sample.mkv"}},
	})
}

func TestSearchPersonConfirmsByWorks(t *testing.T) {
	conn := plugintest.Start(t, plugintest.Build(t, "."), plugintest.Options{})
	c := pluginv1.NewMetadataProviderClient(conn)
	search := func(works ...string) *pluginv1.SearchPersonResponse {
		req := &pluginv1.SearchPersonRequest{Name: "Sample Person"}
		for _, w := range works {
			req.Works = append(req.Works, &pluginv1.PersonWork{Name: w, ItemType: pluginv1.ItemType_ITEM_TYPE_MOVIE})
		}
		resp, err := c.SearchPerson(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if got := search("abc123"); len(got.GetCandidates()) != 1 || !got.GetCandidates()[0].GetConfirmed() {
		t.Errorf("a matching work should confirm the person: %v", got)
	}
	if got := search("Other Title"); len(got.GetCandidates()) != 1 || got.GetCandidates()[0].GetConfirmed() {
		t.Errorf("a name alone must not confirm the person: %v", got)
	}
}
