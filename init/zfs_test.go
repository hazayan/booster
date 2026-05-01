package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestZfsRawKeyStdinMaterial(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		keyformat string
		key       []byte
		want      []byte
		wantErr   string
	}{
		{
			name:      "passphrase utf8",
			keyformat: "passphrase",
			key:       []byte("correct horse battery staple"),
			want:      []byte("correct horse battery staple"),
		},
		{
			name:      "passphrase binary",
			keyformat: "passphrase",
			key:       []byte{0xff, 0x00, 0x01},
			want:      []byte("ff0001"),
		},
		{
			name:      "hex",
			keyformat: "hex",
			key:       []byte{0x12, 0x34, 0xab, 0xcd},
			want:      []byte("1234abcd"),
		},
		{
			name:      "raw",
			keyformat: "raw",
			key:       bytes.Repeat([]byte{0x42}, 32),
			want:      bytes.Repeat([]byte{0x42}, 32),
		},
		{
			name:      "raw rejects wrong length",
			keyformat: "raw",
			key:       bytes.Repeat([]byte{0x42}, 31),
			wantErr:   "raw ZFS keys must be 32 bytes",
		},
		{
			name:      "unsupported format",
			keyformat: "bogus",
			key:       []byte("secret"),
			wantErr:   `unsupported ZFS keyformat "bogus"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := zfsRawKeyStdinMaterial(tt.keyformat, tt.key)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestZfsClevisKeyStdinMaterial(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		clevisKeyFormat string
		zfsKeyformat    string
		key             []byte
		want            []byte
		wantErr         string
	}{
		{
			name:            "plaintext passphrase",
			clevisKeyFormat: "plaintext",
			zfsKeyformat:    "passphrase",
			key:             []byte("secret"),
			want:            []byte("secret"),
		},
		{
			name:            "plaintext hex",
			clevisKeyFormat: "plaintext",
			zfsKeyformat:    "hex",
			key:             []byte("1234abcd"),
			want:            []byte("1234abcd"),
		},
		{
			name:            "empty format defaults to plaintext",
			clevisKeyFormat: "",
			zfsKeyformat:    "passphrase",
			key:             []byte("secret"),
			want:            []byte("secret"),
		},
		{
			name:            "plaintext rejects raw",
			clevisKeyFormat: "plaintext",
			zfsKeyformat:    "raw",
			key:             []byte("secret"),
			wantErr:         `ZFS clevis key format "plaintext" cannot be used with raw ZFS keys`,
		},
		{
			name:            "raw adapts to zfs keyformat",
			clevisKeyFormat: "raw",
			zfsKeyformat:    "hex",
			key:             []byte{0x12, 0x34, 0xab, 0xcd},
			want:            []byte("1234abcd"),
		},
		{
			name:            "unsupported clevis key format",
			clevisKeyFormat: "bogus",
			zfsKeyformat:    "passphrase",
			key:             []byte("secret"),
			wantErr:         `unsupported ZFS clevis key format "bogus"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := zfsClevisKeyStdinMaterial(tt.clevisKeyFormat, tt.zfsKeyformat, tt.key)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestZfsKeyStdinMaterialReturnsCopy(t *testing.T) {
	t.Parallel()

	rawKey := bytes.Repeat([]byte{0x42}, 32)
	rawMaterial, err := zfsRawKeyStdinMaterial("raw", rawKey)
	require.NoError(t, err)
	rawKey[0] = 0x99
	require.Equal(t, byte(0x42), rawMaterial[0])

	passphraseKey := []byte("secret")
	passphraseMaterial, err := zfsClevisKeyStdinMaterial("plaintext", "passphrase", passphraseKey)
	require.NoError(t, err)
	passphraseKey[0] = 'x'
	require.Equal(t, []byte("secret"), passphraseMaterial)
}

func TestZfsKeyUsesTextLines(t *testing.T) {
	t.Parallel()

	require.True(t, zfsKeyUsesTextLines("passphrase"))
	require.True(t, zfsKeyUsesTextLines("hex"))
	require.False(t, zfsKeyUsesTextLines("raw"))
}
