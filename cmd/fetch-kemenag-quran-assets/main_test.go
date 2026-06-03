package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseFootnotes(t *testing.T) {
	t.Parallel()

	raw := "1) Catatan pertama. 2) Catatan kedua."

	footnotes := parseFootnotes(&raw)

	assert.Equal(t, []footnoteRow{
		{Number: 1, Marker: "1)", Text: "Catatan pertama."},
		{Number: 2, Marker: "2)", Text: "Catatan kedua."},
	}, footnotes)
}
