package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReadEmptyConfig(t *testing.T) {
	t.Parallel()

	c, err := readGeneratorConfig("")
	require.NoError(t, err)
	require.Equal(t, "zstd", c.compression)
}

func TestParseCommaList(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"mod1", "mod2"}, parseCommaList(" mod1, ,mod2 ,, "))
	require.Nil(t, parseCommaList(" , , "))
}

func TestReadConfigNormalizesCommaSeparatedFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "booster.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
modules: " dm_mod, , nvme "
modules_force_load: " usbhid, hid_generic "
extra_files: " /bin/ls, , /bin/cat "
network:
  dhcp: true
  interfaces: " aa:bb:cc:dd:ee:ff, , 11:22:33:44:55:66 "
`), 0o644))

	c, err := readGeneratorConfig(cfgPath)
	require.NoError(t, err)
	require.Equal(t, []string{"dm_mod", "nvme"}, c.modules)
	require.Equal(t, []string{"usbhid", "hid_generic"}, c.modulesForceLoad)
	require.Equal(t, []string{"/bin/ls", "/bin/cat"}, c.extraFiles)
	require.Len(t, c.networkActiveInterfaces, 2)
}

func TestReadConfigZfsClevisAttrs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "booster.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
enable_zfs: true
zfs_clevis_attrs:
  jwe: site:clevis_jwe
  pin: site:clevis_pin
zfs_clevis_key_format: raw
zfs_clevis_timeout: 45s
`), 0o644))

	c, err := readGeneratorConfig(cfgPath)
	require.NoError(t, err)
	require.True(t, c.enableZfs)
	require.Equal(t, "site:clevis_jwe", c.zfsClevisJweAttr)
	require.Equal(t, "site:clevis_pin", c.zfsClevisPinAttr)
	require.Equal(t, "raw", c.zfsClevisKeyFormat)
	require.Equal(t, 45*time.Second, c.zfsClevisTimeout)
}

func TestReadConfigZfsClevisRejectsInvalidKeyFormat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "booster.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
zfs_clevis_key_format: bogus
`), 0o644))

	_, err := readGeneratorConfig(cfgPath)
	require.ErrorContains(t, err, `Unsupported ZFS clevis key format "bogus"`)
}

func TestReadConfigZfsClevisRejectsNonPositiveTimeout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "booster.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
zfs_clevis_timeout: 0s
`), 0o644))

	_, err := readGeneratorConfig(cfgPath)
	require.ErrorContains(t, err, "ZFS clevis timeout must be positive")
}
