package handler

import (
	"strings"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/stretchr/testify/require"
)

func TestValidateSendFieldsLimits(t *testing.T) {
	for _, field := range []string{"body_text", "body_html", "in_reply_to", "references"} {
		t.Run(field, func(t *testing.T) {
			limit := 998
			if strings.HasPrefix(field, "body_") {
				limit = 8388608
			}
			for _, extra := range []int{0, 1} {
				text := strings.Repeat("é", limit+extra)
				var bodyText, bodyHTML, reply api.OptString
				var refs []string
				switch field {
				case "body_text":
					bodyText = api.NewOptString(text)
				case "body_html":
					bodyHTML = api.NewOptString(text)
				case "in_reply_to":
					reply = api.NewOptString(text)
				case "references":
					refs = []string{text}
				}
				_, err := validateSendFields(api.OptString{}, api.OptString{}, api.OptString{}, api.OptString{}, api.OptString{}, bodyText, bodyHTML, reply, refs)
				if extra == 0 {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
			}
		})
	}
}

func TestValidateReferenceCountLimit(t *testing.T) {
	for _, count := range []int{1000, 1001} {
		refs := make([]string, count)
		for i := range refs {
			refs[i] = "id@example.com"
		}
		_, err := validateSendFields(api.OptString{}, api.OptString{}, api.OptString{}, api.OptString{}, api.OptString{}, api.OptString{}, api.OptString{}, api.OptString{}, refs)
		if count == 1000 {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
}
