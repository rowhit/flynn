package installer

import (
	"crypto/md5"
	"crypto/rsa"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/flynn/flynn/Godeps/_workspace/src/code.google.com/p/goauth2/oauth"
	"github.com/flynn/flynn/Godeps/_workspace/src/github.com/digitalocean/godo"
	"github.com/flynn/flynn/Godeps/_workspace/src/golang.org/x/crypto/ssh"
	"github.com/flynn/flynn/pkg/sshkeygen"
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
	c.base.SSHUsername = "root"
	if err := c.base.SetDefaultsAndValidate(); err != nil {
		return err
	}
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
		defer c.base.handleDone()

		steps := []func() error{
			c.createKeyPair,
			c.base.allocateDomain,
			c.configureDNS,
			c.createDroplet,
			c.fetchInstanceIPs,
			c.configureDomain,
			c.installFlynn,
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

		if err := c.base.configureCLI(); err != nil {
			c.base.SendLog(fmt.Sprintf("WARNING: Failed to configure CLI: %s", err))
		}
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

func (c *DigitalOceanCluster) configureDNS() error {
	c.base.SendLog("Configuring DNS")
	nameServers := []string{
		"ns1.digitalocean.com",
		"ns2.digitalocean.com",
		"ns3.digitalocean.com",
	}
	if err := c.base.Domain.Configure(nameServers); err != nil {
		return err
	}
	return nil
}

func (c *DigitalOceanCluster) createDroplet() error {
	c.base.SendLog(fmt.Sprintf("Creating droplet %s", c.base.Name))
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
	for _, a := range dr.Links.Actions {
		if a.Rel == "create" {
			return c.waitForActionComplete(a.ID)
		}
	}
	return errors.New("Unable to locate create action ID")
}

func (c *DigitalOceanCluster) waitForActionComplete(actionID int) error {
	fetchAction := func() (*godo.Action, error) {
		action, _, err := c.client.Actions.Get(actionID)
		if err != nil {
			return nil, err
		}
		return action, nil
	}
	for {
		action, err := fetchAction()
		if err != nil {
			return err
		}
		switch action.Status {
		case "completed":
			return nil
		case "errored":
			return errors.New("Droplet create failed")
		}
		time.Sleep(1 * time.Second)
	}
}

func (c *DigitalOceanCluster) fetchInstanceIPs() error {
	if err := c.fetchDroplet(); err != nil {
		return err
	}
	instanceIPs := make([]string, len(c.droplet.Networks.V4))
	for i, n := range c.droplet.Networks.V4 {
		instanceIPs[i] = n.IPAddress
	}
	if int64(len(instanceIPs)) != c.base.NumInstances {
		return fmt.Errorf("Expected %d instances, but found %d", c.base.NumInstances, len(instanceIPs))
	}
	c.base.InstanceIPs = instanceIPs
	if err := c.base.saveInstanceIPs(); err != nil {
		return err
	}
	return nil
}

func (c *DigitalOceanCluster) configureDomain() error {
	c.base.SendLog("Configuring domain")
	instanceIP := c.base.InstanceIPs[0]
	dr, _, err := c.client.Domains.Create(&godo.DomainCreateRequest{
		Name:      c.base.Domain.Name,
		IPAddress: instanceIP,
	})
	if err != nil {
		return err
	}
	domain := dr.Domain
	for i, ip := range c.base.InstanceIPs {
		if i == 0 {
			// An A record already exists via the create domain request
			continue
		}
		_, _, err := c.client.Domains.CreateRecord(domain.Name, &godo.DomainRecordEditRequest{
			Type: "A",
			Name: fmt.Sprintf("%s.", domain.Name),
			Data: ip,
		})
		if err != nil {
			return err
		}
	}
	_, _, err = c.client.Domains.CreateRecord(domain.Name, &godo.DomainRecordEditRequest{
		Type: "CNAME",
		Name: fmt.Sprintf("*.%s.", domain.Name),
		Data: fmt.Sprintf("%s.", domain.Name),
	})
	return err
}

func (c *DigitalOceanCluster) installFlynn() error {
	c.base.SendLog("Installing flynn")
	sshConfig, err := c.base.sshConfig()
	if err != nil {
		return err
	}

	instanceIPs := c.base.InstanceIPs
	done := make(chan struct{})
	errChan := make(chan error)
	ops := []func(*ssh.ClientConfig, string) error{
		c.instanceWaitForSSH,
		c.instanceInstallFlynn,
		c.instanceStartFlynn,
	}
	for _, op := range ops {
		for _, ipAddress := range instanceIPs {
			go func() {
				err := op(sshConfig, ipAddress)
				if err != nil {
					errChan <- err
					return
				}
				done <- struct{}{}
			}()
		}
		for _ = range instanceIPs {
			select {
			case <-done:
			case err := <-errChan:
				return err
			}
		}
	}
	return nil
}

func (c *DigitalOceanCluster) instanceWaitForSSH(sshConfig *ssh.ClientConfig, ipAddress string) error {
	c.base.SendLog(fmt.Sprintf("Waiting for ssh on %s", ipAddress))
	timeout := time.After(5 * time.Minute)
	for {
		sshConn, err := ssh.Dial("tcp", ipAddress+":22", sshConfig)
		if err != nil {
			if _, ok := err.(*net.OpError); ok {
				select {
				case <-time.After(5 * time.Second):
					continue
				case <-timeout:
					return err
				}
			}
			return err
		}
		sshConn.Close()
		return nil
	}
}

func (c *DigitalOceanCluster) instanceInstallFlynn(sshConfig *ssh.ClientConfig, ipAddress string) error {
	c.base.SendLog(fmt.Sprintf("Installing flynn on %s", ipAddress))
	cmd := "sudo bash < <(curl -fsSL https://dl.flynn.io/install-flynn)"
	return c.base.instanceRunCmd(cmd, sshConfig, ipAddress)
}

func (c *DigitalOceanCluster) instanceStartFlynn(sshConfig *ssh.ClientConfig, ipAddress string) error {
	c.base.SendLog(fmt.Sprintf("Starting flynn on %s", ipAddress))
	cmd := "sudo start flynn-host"
	return c.base.instanceRunCmd(cmd, sshConfig, ipAddress)
}

func (c *DigitalOceanCluster) fetchDroplet() error {
	c.base.SendLog(fmt.Sprintf("Fetching droplet %s", c.base.Name))
	dr, _, err := c.client.Droplets.Get(int(c.DropletID))
	if err != nil {
		return err
	}
	c.droplet = dr.Droplet
	return nil
}

func (c *DigitalOceanCluster) bootstrap() error {
	return c.base.bootstrap()
}

func (c *DigitalOceanCluster) Delete() {
	prevState := c.base.State
	c.base.setState("deleting")

	if c.base.Domain != nil {
		if _, err := c.client.Domains.Delete(c.base.Domain.Name); err != nil {
			c.base.SendError(err)
		}
	} else {
		c.base.SendLog("Skipping domain deletion")
	}

	if c.DropletID != 0 {
		if _, err := c.client.Droplets.Delete(int(c.DropletID)); err != nil {
			c.base.SendError(err)
			c.base.setState(prevState)
			return
		}
	} else {
		c.base.SendLog("Skipping droplet deletion")
	}

	if err := c.base.MarkDeleted(); err != nil {
		c.base.SendError(err)
	}
	c.base.sendEvent(&Event{
		ClusterID:   c.base.ID,
		Type:        "cluster_state",
		Description: "deleted",
	})
}
