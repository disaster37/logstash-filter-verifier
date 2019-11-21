package testcase

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertIntToFloat64(t *testing.T) {

	data := map[string]interface{}{
		"key1": "plop",
		"key2": int(10),
		"key3": map[string]interface{}{
			"key31": "plop",
			"Key32": float64(1),
			"Key33": int(3),
		},
	}

	expected := map[string]interface{}{
		"key1": "plop",
		"key2": float64(10),
		"key3": map[string]interface{}{
			"key31": "plop",
			"Key32": float64(1),
			"Key33": float64(3),
		},
	}

	assert.Equal(t, convertIntToFloat64(data), expected)

}
