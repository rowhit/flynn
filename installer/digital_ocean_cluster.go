package installer

import (
	"crypto/md5"
	"crypto/rsa"
	"fmt"
	"strings"

	"github.com/flynn/flynn/Godeps/_workspace/src/code.google.com/p/goauth2/oauth"
	"github.com/flynn/flynn/Godeps/_workspace/src/github.com/digitalocean/godo"
	"github.com/flynn/flynn/pkg/sshkeygen"
	"golang.org/x/crypto/ssh"
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

func (c *DigitalOceanCluster) saveField(field string, value interface{}) error {
	c.base.installer.dbMtx.Lock()
	defer c.base.installer.dbMtx.Unlock()
	return c.base.installer.txExec(fmt.Sprintf(`
  UPDATE digital_ocean_clusters SET %s = $2 WHERE ClusterID == $1
  `, field), c.ClusterID, value)
}

func (c *DigitalOceanCluster) Run() {
	go func() {
		// defer c.base.handleDone()

		steps := []func() error{
			c.createKeyPair,
			c.base.allocateDomain,
			c.createDroplet,
			c.installFlynn,
			c.configureDNS,
			c.bootstrap,
		}

		for _, step := range steps {
			if err := step(); err != nil {
				if c.base.State != "deleting" {
					c.base.setState("error")
					c.base.SendError(err)
				}
				return
			}
		}

		c.base.setState("running")

		/*
			if err := c.base.configureCLI(); err != nil {
				c.base.SendLog(fmt.Sprintf("WARNING: Failed to configure CLI: %s", err))
			}
		*/
	}()
}

func (c *DigitalOceanCluster) createKeyPair() error {
	keypairName := "flynn"
	if c.base.SSHKeyName != "" {
		keypairName = c.base.SSHKeyName
	}
	if err := c.loadKeyPair(keypairName); err == nil {
		c.base.SendLog(fmt.Sprintf("Using saved key pair (%s)", c.base.SSHKeyName))
		return nil
	}

	keypair, err := loadSSHKey(keypairName)
	if err == nil {
		c.base.SendLog("Importing key pair")
	} else {
		c.base.SendLog("Creating key pair")
		keypair, err = sshkeygen.Generate()
		if err != nil {
			return err
		}
	}

	key, _, err := c.client.Keys.Create(&godo.KeyCreateRequest{
		Name:      keypairName,
		PublicKey: string(keypair.PublicKey),
	})
	if err != nil {
		fmt.Printf("Err creating key pair: %T(%s)\n", err, err)
		return err
	}

	c.base.SSHKey = keypair
	c.base.SSHKeyName = keypairName
	c.KeyFingerprint = key.Fingerprint
	if err := c.saveField("KeyFingerprint", c.KeyFingerprint); err != nil {
		return err
	}

	err = saveSSHKey(keypairName, keypair)
	if err != nil {
		return err
	}
	return nil
}

func (c *DigitalOceanCluster) loadKeyPair(name string) error {
	keypair, err := loadSSHKey(name)
	if err != nil {
		return err
	}
	fingerprint, err := c.fingerprintSSHKey(keypair.PrivateKey)
	key, _, err := c.client.Keys.GetByFingerprint(fingerprint)
	if err != nil {
		return err
	}
	c.base.SSHKey = keypair
	c.base.SSHKeyName = key.Name
	c.KeyFingerprint = fingerprint
	if err := c.saveField("KeyFingerprint", c.KeyFingerprint); err != nil {
		return err
	}
	return saveSSHKey(c.base.SSHKeyName, keypair)
}

func (c *DigitalOceanCluster) fingerprintSSHKey(privateKey *rsa.PrivateKey) (string, error) {
	rsaPubKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", err
	}
	md5Data := md5.Sum(rsaPubKey.Marshal())
	strbytes := make([]string, len(md5Data))
	for i, b := range md5Data {
		strbytes[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(strbytes, ":"), nil
}

func (c *DigitalOceanCluster) createDroplet() error {
	c.base.SendLog("Creating droplet")
	dr, _, err := c.client.Droplets.Create(&godo.DropletCreateRequest{
		Name:   c.base.Name,
		Region: c.Region,
		Size:   c.Size,
		Image: godo.DropletCreateImage{
			Slug: "ubuntu-14-04-x64",
		},
		SSHKeys: []godo.DropletCreateSSHKey{{
			Fingerprint: c.KeyFingerprint,
		}},
	})
	if err != nil {
		return err
	}
	droplet := dr.Droplet
	c.DropletID = int64(droplet.ID)
	if err := c.saveField("DropletID", c.DropletID); err != nil {
		return err
	}
	fmt.Printf("%#v\n", droplet)
	return nil
}

func (c *DigitalOceanCluster) installFlynn() error {
	return nil
}

func (c *DigitalOceanCluster) configureDNS() error {
	return nil
}

func (c *DigitalOceanCluster) bootstrap() error {
	return nil
}

func (c *DigitalOceanCluster) Delete() {
	c.base.setState("deleting")
	if err := c.base.MarkDeleted(); err != nil {
		c.base.SendError(err)
	}
	c.base.sendEvent(&Event{
		ClusterID:   c.base.ID,
		Type:        "cluster_state",
		Description: "deleted",
	})
}
