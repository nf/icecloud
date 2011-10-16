package main

import (
	"bytes"
	"exec"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"json"
	"launchpad.net/goamz/aws"
	"launchpad.net/goamz/ec2"
	"log"
	"os"
	"time"
)

type Config struct {
	KeyName string
	Server  []*Server
	Icecast *Icecast

	auth aws.Auth
}

type Icecast struct {
	SourcePassword string
	RelayPassword  string
	AdminPassword  string
	ListenPort     int
}

type Server struct {
	Name string

	Kind     string // "master" or "slave"
	Location string // to be translated through the Locations map

	Username string // login name
	ImageID  string // must be available at this location
	Size     string // something like "t1.micro"

	NumClients, NumSources int // numbers of icecast clients and sources

	Instance *ec2.Instance
}

func (s *Server) Region() aws.Region {
	r, ok := Locations[s.Location]
	if !ok {
		panic(fmt.Sprintf("invalid Server Location: %q", s.Location))
	}
	return r
}

func (s *Server) String() string {
	a := fmt.Sprintf("%s %s", s.Kind, s.Location)
	if s.Instance != nil {
		a += fmt.Sprintf(" (%s) (%s)",
			s.Instance.InstanceId,
			s.Instance.DNSName,
		)
	}
	return a
}

func (c *Config) ServerURL(s *Server) string {
	if s.Instance == nil || c.Icecast == nil {
		return ""
	}
	return fmt.Sprintf("http://%s:%d/", s.Instance.DNSName, c.Icecast.ListenPort)
}

var Locations = map[string]aws.Region{
	"Tokyo":     aws.APNortheast,
	"Singapore": aws.APSoutheast,
	"Europe":    aws.EUWest,
	"USEast":    aws.USEast,
	"USWest":    aws.USWest,
}

func ReadConfig(filename string) (*Config, os.Error) {
	b, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	c := new(Config)
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}

	auth, err := aws.EnvAuth()
	if err != nil {
		return nil, err
	}
	c.auth = auth

	return c, nil
}

func (c *Config) Write(filename string) os.Error {
	b, err := json.MarshalIndent(c, "", "\t")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filename, b, 0600)
}

func (c *Config) Run() os.Error {
	for _, s := range c.Server {
		if err := c.runInstance(s); err != nil {
			log.Println("run:", err)
			log.Println("trying to shut down")
			return c.Shutdown()
		}
	}
	done := make(chan *Server)
	for _, s := range c.Server {
		go func(s *Server) {
			if err := c.waitReady(s); err != nil {
				log.Printf("%v: %v", s, err)
			} else {
				log.Printf("%v: ready", s)
			}
			done <- s
		}(s)
	}
	for _ = range c.Server {
		<-done
	}
	return nil
}

func (c *Config) runInstance(s *Server) os.Error {
	e := ec2.New(c.auth, s.Region())
	options := &ec2.RunInstances{
		ImageId:      s.ImageID,
		InstanceType: s.Size,
		KeyName:      c.KeyName,
	}
	resp, err := e.RunInstances(options)
	if err != nil {
		return err
	}
	if len(resp.Instances) != 1 {
		return fmt.Errorf("want 1 instance, got %d", len(resp.Instances))
	}
	s.Instance = &resp.Instances[0]
	return nil
}

func (c *Config) waitReady(s *Server) os.Error {
	deadline := time.Seconds() + 120e9 // 2 minute deadline
	for time.Seconds() < deadline {
		inst, err := c.getInstance(s)
		if err != nil {
			return err
		}
		if inst.DNSName != "" {
			return nil
		}
		time.Sleep(5e9)
	}
	return os.NewError("waitReady: server took too long")
}

func (c *Config) getInstance(s *Server) (*ec2.Instance, os.Error) {
	e := ec2.New(c.auth, s.Region())
	instIds := []string{s.Instance.InstanceId}
	resp, err := e.Instances(instIds, nil)
	if err != nil {
		return nil, err
	}
	if len(resp.Reservations) != 1 {
		return nil, fmt.Errorf("getInstance: want 1 reservation, got %d", len(resp.Reservations))
	}
	r := resp.Reservations[0]
	if len(r.Instances) != 1 {
		return nil, fmt.Errorf("getInstance: want 1 instance, got %d", len(r.Instances))
	}
	s.Instance = &r.Instances[0]
	return &r.Instances[0], nil
}

func (c *Config) Shutdown() os.Error {
	ok := true
	for _, s := range c.Server {
		if s.Instance == nil {
			continue
		}
		e := ec2.New(c.auth, s.Region())
		instIds := []string{s.Instance.InstanceId}
		_, err := e.TerminateInstances(instIds)
		if err != nil {
			log.Println(s.Instance.InstanceId, err)
			ok = false
		}
	}
	if !ok {
		return os.NewError("some instances didn't shut down cleanly")
	}
	return nil
}

func (c *Config) Setup() os.Error {
	ok := make(chan bool)
	for _, s := range c.Server {
		go func(s *Server) {
			err := c.setupInstance(s)
			if err != nil {
				log.Printf("%v: %v", s, err)
				ok <- false
			} else {
				log.Printf("%v: online", s)
				ok <- true
			}
		}(s)
	}
	allOk := true
	for _ = range c.Server {
		k := <-ok
		allOk = allOk && k
	}
	if !allOk {
		return os.NewError("some instances didn't set up cleanly")
	}
	return nil
}

func (c *Config) setupInstance(s *Server) os.Error {
	// create the setup.sh script
	stdin := new(bytes.Buffer)
	var err os.Error
	if s.Kind == "master" {
		err = SetupTemplate(stdin, c.Icecast, s, nil)
	} else {
		var m *Server
		for _, n := range c.Server {
			if n.Kind == "master" {
				m = n
				break
			}
		}
		if m == nil {
			return os.NewError("no master found in config")
		}
		err = SetupTemplate(stdin, c.Icecast, s, m)
	}
	if err != nil {
		return err
	}
	err = c.sshCommand(s, "cat > setup.sh", stdin)
	if err != nil {
		return err
	}

	// run it
	return c.sshCommand(s, "bash setup.sh", nil)
}

func (c *Config) sshCommand(s *Server, command string, stdin io.Reader) os.Error {
	if s.Instance == nil {
		return os.NewError("sshCommand: nil instance")
	}
	userhost := fmt.Sprintf("%s@%s", s.Username, s.Instance.DNSName)
	cmd := exec.Command("ssh", "-v", "-o", "StrictHostKeyChecking=no", userhost, command)
	cmd.Stdin = stdin
	b, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("%v: %s\n%s", s, command, b)
	}
	return err
}

func (c *Config) Playlist(mount []string) os.Error {
	for _, m := range mount {
		if err := c.writePlaylist(m); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) writePlaylist(mount string) os.Error {
	for i, s := range c.Server {
		if s.Kind == "master" {
			continue
		}
		fn := fmt.Sprintf("%s-%s.m3u", mount, s.Name)
		f, err := os.Create(fn)
		if err != nil {
			return err
		}
		fmt.Fprintf(f, "%s%s\n", c.ServerURL(s), mount)
		for j, s := range c.Server {
			if s.Kind == "master" || i == j {
				continue
			}
			fmt.Fprintf(f, "%s%s\n", c.ServerURL(s), mount)
		}
		f.Close()
	}
	return nil
}

func main() {
	stateFile := flag.String("state", "state.json", "file in which to store system state")
	flag.Parse()
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %v run configfile\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "       %v setup\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "       %v playlist\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "       %v shutdown\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "flags:")
		flag.PrintDefaults()
		os.Exit(1)
	}
	verb := flag.Arg(0)
	if verb == "" {
		flag.Usage()
	}

	configFile := flag.Arg(1)
	if verb != "run" {
		configFile = *stateFile
	} else if configFile == "" {
		flag.Usage()
	}
	config, err := ReadConfig(configFile)
	if err != nil {
		log.Fatal(err)
	}

	switch verb {
	case "run":
		err = config.Run()
	case "setup":
		err = config.Setup()
	case "playlist":
		err = config.Playlist(flag.Args()[1:])
	case "shutdown":
		err = config.Shutdown()
	default:
		err = os.NewError("invalid verb")
	}
	if err != nil {
		log.Fatal(err)
	}

	if err := config.Write(*stateFile); err != nil {
		log.Fatal(err)
	}
}
