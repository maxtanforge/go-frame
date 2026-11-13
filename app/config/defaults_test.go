package config

import (
	"testing"

	"github.com/maxtanforge/go-frame/identity"
	"github.com/stretchr/testify/require"
)

// rand.Text 输出 base32 字母表，identity.New 对常量输入当前不可能失败。
// 本测试锁定 Defaults 的对外契约：不依赖环境、InstanceID 非空且合法，
// 为移除 panic 分支后的退化路径提供回归锚点。
func TestDefaultsInstanceIDAlwaysValid(t *testing.T) {
	c := Defaults()
	require.NotEmpty(t, c.Service.InstanceID)
	require.NoError(t, identity.ValidateInstanceID(c.Service.InstanceID))
}
