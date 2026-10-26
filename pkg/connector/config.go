package connector

import (
	_ "embed"
	"strings"
	"text/template"
	"time"

	up "go.mau.fi/util/configupgrade"
	"gopkg.in/yaml.v3"
)

//go:embed example-config.yaml
var ExampleConfig string

type Config struct {
	DisplaynameTemplate    string        `yaml:"displayname_template"`
	InitialChatSyncCount   int           `yaml:"initial_chat_sync_count"`
	ContactRefreshInterval time.Duration `yaml:"contact_refresh_interval"`

	displaynameTemplate *template.Template `yaml:"-"`
}

type umConfig Config

func (c *Config) UnmarshalYAML(node *yaml.Node) error {
	err := node.Decode((*umConfig)(c))
	if err != nil {
		return err
	}
	c.displaynameTemplate, err = template.New("displayname").Parse(c.DisplaynameTemplate)
	return err
}

type DisplaynameTemplateArgs struct {
	Name    string
	Address string
}

func (c *Config) FormatDisplayname(name, address string) string {
	var buf strings.Builder
	_ = c.displaynameTemplate.Execute(&buf, DisplaynameTemplateArgs{Name: name, Address: address})
	return buf.String()
}

func (ic *IMConnector) GetConfig() (example string, data any, upgrader up.Upgrader) {
	return ExampleConfig, &ic.Config, up.SimpleUpgrader(upgradeConfig)
}

func upgradeConfig(helper up.Helper) {
	helper.Copy(up.Str, "displayname_template")
	helper.Copy(up.Int, "initial_chat_sync_count")
	helper.Copy(up.Str|up.Int, "contact_refresh_interval")
}
