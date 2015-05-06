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

func (c *DigitalOceanCluster) Run() {
	go func() {
		// defer c.base.handleDone()

		steps := []func() error{
			c.createKeyPair,
			// - Allocate domain
			// - Create droplet with ubuntu image
			// - Run flynn installer on droplet
			// - Configure DNS / Create domain record
			// - Bootstrap layer 1
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

	if _, _, err := c.client.Keys.Create(&godo.KeyCreateRequest{
		Name:      keypairName,
		PublicKey: string(keypair.PublicKey),
	}); err != nil {
		fmt.Printf("Err creating key pair: %T(%s)\n", err, err)
		return err
	}
	if err != nil {
		return err
	}

	c.base.SSHKey = keypair
	c.base.SSHKeyName = keypairName

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
