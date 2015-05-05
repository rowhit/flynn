package installer

import (
	"github.com/flynn/flynn/Godeps/_workspace/src/code.google.com/p/goauth2/oauth"
	"github.com/flynn/flynn/Godeps/_workspace/src/github.com/digitalocean/godo"
)

func (c *DigitalOceanCluster) Type() string {
	const t = "digital_ocean"
	return t
}

func (c *DigitalOceanCluster) Base() *BaseCluster {
	return c.base
}

func (c *DigitalOceanCluster) SetBase(base *BaseCluster) {
	c.base = base
}

func (c *DigitalOceanCluster) SetCreds(creds *Credential) error {
	c.base.credential = creds
	c.base.CredentialID = creds.ID
	t := &oauth.Transport{
		Token: &oauth.Token{AccessToken: creds.Secret},
	}
	c.client = godo.NewClient(t.Client())
	return nil
}

func (c *DigitalOceanCluster) SetDefaultsAndValidate() error {
	c.ClusterID = c.base.ID
	return nil
}

func (c *DigitalOceanCluster) Run() {
}

func (c *DigitalOceanCluster) Delete() {
}
