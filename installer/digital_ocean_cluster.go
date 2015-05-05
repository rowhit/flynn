package installer

import (
	"github.com/flynn/flynn/Godeps/_workspace/src/code.google.com/p/goauth2/oauth"
	"github.com/flynn/flynn/Godeps/_workspace/src/github.com/digitalocean/godo"
)

func digitalOceanClient(creds *Credential) *godo.Client {
	t := &oauth.Transport{
		Token: &oauth.Token{AccessToken: creds.Secret},
	}
	return godo.NewClient(t.Client())
}

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
	c.client = digitalOceanClient(creds)
	return nil
}

func (c *DigitalOceanCluster) SetDefaultsAndValidate() error {
	c.ClusterID = c.base.ID
	return nil
}

func (c *DigitalOceanCluster) Run() {
	// - Create key pair
	// - Allocate domain
	// - Create droplet with ubuntu image
	// - Run flynn installer on droplet
	// - Configure DNS / Create domain record
	// - Bootstrap layer 1
}

func (c *DigitalOceanCluster) Delete() {
}
