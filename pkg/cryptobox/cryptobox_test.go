package cryptobox_test

import (
	"strings"
	"testing"

	"github.com/alfariesh/surau-backend/pkg/cryptobox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSeed = "0123456789abcdef0123456789abcdef-seed"

func TestSealOpenRoundTrip(t *testing.T) {
	t.Parallel()

	box, err := cryptobox.New(testSeed, "test-v1")
	require.NoError(t, err)

	for _, plaintext := range []string{"", "s", "JBSWY3DPEHPK3PXP", strings.Repeat("x", 4096)} {
		sealed, err := box.Seal([]byte(plaintext))
		require.NoError(t, err)

		opened, err := box.Open(sealed)
		require.NoError(t, err)
		assert.Equal(t, plaintext, string(opened))
	}
}

func TestSealIsNonDeterministic(t *testing.T) {
	t.Parallel()

	box, err := cryptobox.New(testSeed, "test-v1")
	require.NoError(t, err)

	first, err := box.Seal([]byte("secret"))
	require.NoError(t, err)

	second, err := box.Seal([]byte("secret"))
	require.NoError(t, err)

	assert.NotEqual(t, first, second, "fresh nonce per seal")
}

func TestOpenRejectsTamperAndGarbage(t *testing.T) {
	t.Parallel()

	box, err := cryptobox.New(testSeed, "test-v1")
	require.NoError(t, err)

	sealed, err := box.Seal([]byte("secret"))
	require.NoError(t, err)

	tampered := []byte(sealed)
	tampered[len(tampered)-1] ^= 0x01

	_, err = box.Open(string(tampered))
	require.ErrorIs(t, err, cryptobox.ErrCiphertext)

	_, err = box.Open("!!!not-base64!!!")
	require.ErrorIs(t, err, cryptobox.ErrCiphertext)

	_, err = box.Open("c2hvcnQ") // valid base64, shorter than a nonce
	require.ErrorIs(t, err, cryptobox.ErrCiphertext)
}

func TestDistinctKeysCannotOpenEachOther(t *testing.T) {
	t.Parallel()

	boxA, err := cryptobox.New(testSeed, "info-a")
	require.NoError(t, err)

	boxB, err := cryptobox.New(testSeed, "info-b")
	require.NoError(t, err)

	otherSeed, err := cryptobox.New(testSeed+"-other-seed-material", "info-a")
	require.NoError(t, err)

	sealed, err := boxA.Seal([]byte("secret"))
	require.NoError(t, err)

	_, err = boxB.Open(sealed)
	require.ErrorIs(t, err, cryptobox.ErrCiphertext, "same seed, different info")

	_, err = otherSeed.Open(sealed)
	require.ErrorIs(t, err, cryptobox.ErrCiphertext, "different seed, same info")
}

func TestNewRejectsShortSeed(t *testing.T) {
	t.Parallel()

	_, err := cryptobox.New("too-short", "test-v1")
	require.ErrorIs(t, err, cryptobox.ErrSeedTooShort)
}
