package config

import (
	"errors"
	"fmt"
	"math"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type decodeText string

func (v *decodeText) UnmarshalText(b []byte) error {
	if string(b) == "bad-secret" {
		return errors.New("bad-secret")
	}
	*v = decodeText(strings.ToUpper(string(b)))
	return nil
}

type invalidRoot struct {
	Password string `secret:"true"`
}

func (*invalidRoot) Validate() error { return errors.New("validation-secret") }
func TestDecodePresenceAndProvenance(t *testing.T) {
	type nested struct {
		Name     string `env:"NAME"`
		Count    int    `env:"COUNT"`
		Enabled  bool
		List     []int
		Wait     time.Duration
		Custom   decodeText
		Password string `secret:"true"`
	}
	type cfg struct {
		Pay nested `envPrefix:"PAY_"`
	}
	defaults := cfg{Pay: nested{Name: "default", Count: 2, Enabled: true, List: []int{9}, Password: "hidden"}}
	r, err := Decode(defaults, Source{Name: "file", Values: map[string]string{"pay.name": "file", "pay.count": "3"}}, Source{Name: "env", Lookup: lookup(map[string]string{"PAY_NAME": "", "PAY_COUNT": "0"})}, Source{Name: "cli", Values: map[string]string{"pay.enabled": "false", "pay.list": "[]", "pay.wait": "2s", "pay.custom": "hello"}})
	require.NoError(t, err)
	require.Empty(t, r.Value.Pay.Name)
	require.Zero(t, r.Value.Pay.Count)
	require.False(t, r.Value.Pay.Enabled)
	require.Empty(t, r.Value.Pay.List)
	require.Equal(t, 2*time.Second, r.Value.Pay.Wait)
	require.Equal(t, decodeText("HELLO"), r.Value.Pay.Custom)
	require.Equal(t, Entry{Path: "pay.name", Env: "PAY_NAME", Source: "env", Value: `""`}, r.Entries[0])
	require.Equal(t, "[REDACTED]", r.Entries[6].Value)
	r2, err := Decode(defaults)
	require.NoError(t, err)
	r2.Value.Pay.List[0] = 1
	require.Equal(t, 9, defaults.Pay.List[0])
}
func TestDecodeRejectsInvalidInputsSafely(t *testing.T) {
	type cfg struct {
		Count  int `env:"COUNT"`
		Custom decodeText
		Float  float64
		List   []int
	}
	for _, values := range []map[string]string{{"count": ""}, {"count": "bad-secret"}, {"custom": "bad-secret"}, {"float": "NaN"}, {"list": "[null]"}, {"list": "[[1]]"}, {"list": "[{}]"}, {"list": "[1.2]"}, {"unknown-secret": "x"}, {"custom": strings.Repeat("x", 65537)}} {
		_, err := Decode(cfg{}, Source{Name: "input", Values: values}, Source{Name: "later", Values: map[string]string{"count": "1"}})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "bad-secret")
		require.NotContains(t, err.Error(), "unknown-secret")
	}
	_, err := Decode(invalidRoot{})
	require.EqualError(t, err, "config: validation failed")
	_, err = Decode(cfg{}, Source{Name: "both", Values: map[string]string{}, Lookup: lookup(nil)})
	require.Error(t, err)
}
func TestDecodeSchemaAndLists(t *testing.T) {
	for _, v := range []any{struct {
		A int `config:"same"`
		B int `config:"same"`
	}{}, struct {
		A int `env:"SAME"`
		B int `env:"SAME"`
	}{}, struct{ A map[string]string }{}, struct{ A *int }{}, struct{ A any }{}, struct{ A [][]string }{}, 42} {
		_, err := Decode(v)
		require.Error(t, err)
	}
	r, err := Decode(struct{ V []time.Duration }{}, Source{Name: "yaml", Values: map[string]string{"v": `["1s","2s"]`}})
	require.NoError(t, err)
	require.Equal(t, []time.Duration{time.Second, 2 * time.Second}, r.Value.V)
	r2, err := Decode(struct{ V []int }{}, Source{Name: "yaml", Values: map[string]string{"v": `[1,"2"]`}})
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, r2.Value.V)
}
func TestDecodeBoundsAndSecretInheritance(t *testing.T) {
	nested := reflect.TypeOf(struct{ Value string }{})
	for i := 0; i < 34; i++ {
		nested = reflect.StructOf([]reflect.StructField{{Name: "Nested", Type: nested}})
	}
	_, err := Decode(reflect.New(nested).Elem().Interface())
	require.Error(t, err)
	fields := make([]reflect.StructField, 4097)
	for i := range fields {
		fields[i] = reflect.StructField{Name: fmt.Sprintf("F%d", i), Type: reflect.TypeOf(0)}
	}
	_, err = Decode(reflect.New(reflect.StructOf(fields)).Elem().Interface())
	require.Error(t, err)
	_, err = Decode(struct {
		A struct{ X string } `config:"same"`
		B struct{ Y string } `config:"same"`
	}{})
	require.Error(t, err)
	r, err := Decode(struct {
		Auth struct{ Password string } `secret:"true"`
	}{}, Source{Name: "test", Values: map[string]string{"auth.password": "sensitive"}})
	require.NoError(t, err)
	require.True(t, r.Entries[0].Secret)
	require.Equal(t, "[REDACTED]", r.Entries[0].Value)
}
func TestDecodeRejectsNonfiniteDefaultsAndBoundsSources(t *testing.T) {
	_, err := Decode(struct{ N float64 }{N: math.NaN()})
	require.Error(t, err)
	sources := make([]Source, 4097)
	for i := range sources {
		sources[i] = Source{Name: "input"}
	}
	_, err = Decode(struct{ N int }{}, sources...)
	require.Error(t, err)
	_, err = Decode(struct {
		N int `env:"NUMBER"`
	}{}, Source{Name: "cli", Values: map[string]string{"n": "bad-secret"}})
	require.ErrorContains(t, err, "cli")
	require.ErrorContains(t, err, "int")
	require.ErrorContains(t, err, "NUMBER")
}
func TestDecodeListWhitespaceAndEmptyStructBounds(t *testing.T) {
	r, err := Decode(struct{ V []int }{}, Source{Name: "input", Values: map[string]string{"v": " \n [1,2] \t "}})
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, r.Value.V)
	fields := make([]reflect.StructField, 4097)
	for i := range fields {
		fields[i] = reflect.StructField{Name: fmt.Sprintf("F%d", i), Type: reflect.TypeOf(struct{}{})}
	}
	_, err = Decode(reflect.New(reflect.StructOf(fields)).Elem().Interface())
	require.Error(t, err)
}
func TestCanonicalCompoundPaths(t *testing.T) {
	defaults := struct {
		Database struct {
			MaxOpenConns int `config:"max_open_conns"`
		}
		Lifecycle struct {
			StartupTimeout time.Duration `config:"startup_timeout"`
		}
		Redis struct {
			TLSKeyFile string `config:"tls_key_file" secret:"true"`
		}
	}{}
	r, err := Decode(defaults, Source{Name: "yaml", Values: map[string]string{"database.max_open_conns": "30", "lifecycle.startup_timeout": "45s", "redis.tls_key_file": ""}})
	require.NoError(t, err)
	require.Equal(t, 30, r.Value.Database.MaxOpenConns)
	require.Equal(t, 45*time.Second, r.Value.Lifecycle.StartupTimeout)
}
func TestDecodeRejectsParentAssignment(t *testing.T) {
	_, err := Decode(struct{ Framework struct{ Name string } }{}, Source{Name: "cli", Values: map[string]string{"framework": "oops"}})
	require.ErrorContains(t, err, "unknown configuration path")
}
func TestDecodeLeavesExcludedFieldsCallerOwned(t *testing.T) {
	defaults := struct {
		Hidden []int `config:"-"`
		Name   string
	}{Hidden: []int{1}}
	result, err := Decode(defaults)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(defaults.Hidden).Pointer(), reflect.ValueOf(result.Value.Hidden).Pointer())
}

type customWithInternals struct{ Values []int }

func (*customWithInternals) UnmarshalText([]byte) error { return nil }
func TestDecodeLeavesCustomInternalsCallerOwned(t *testing.T) {
	defaults := struct{ Custom customWithInternals }{Custom: customWithInternals{Values: []int{1}}}
	result, err := Decode(defaults)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(defaults.Custom.Values).Pointer(), reflect.ValueOf(result.Value.Custom.Values).Pointer())
}

func TestDecodeTextUnmarshalerPrecedesSlice(t *testing.T) {
	r, err := Decode(struct {
		IP  net.IP
		IPs []net.IP
	}{}, Source{Name: "environment", Values: map[string]string{"ip": "127.0.0.1", "ips": `["::1","127.0.0.1"]`}})
	require.NoError(t, err)
	require.True(t, r.Value.IP.Equal(net.ParseIP("127.0.0.1")))
	require.True(t, r.Value.IPs[0].Equal(net.ParseIP("::1")))
	_, err = Decode(struct{ IP net.IP }{}, Source{Name: "environment", Values: map[string]string{"ip": "bad-secret"}})
	require.ErrorContains(t, err, "invalid text value")
	require.NotContains(t, err.Error(), "bad-secret")
}

func lookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}
