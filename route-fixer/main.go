package main

import (
	"encoding/json"
	"io/ioutil"
	"net"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

type NasPartition struct {
	Ip    string `json:"ip"`
	Name  string `json:"name"`
	NasHa string `json:"nasha"`
}

func OptionalEnv(envName string, defaultValue string) string {
	env, isSet := os.LookupEnv(envName)
	if !isSet || len(env) == 0 {
		return defaultValue
	}
	return env
}

func isRoutedVia(via net.IP, ip net.IP) bool {
	routes, err := netlink.RouteGet(ip)
	if err != nil {
		logrus.Errorf("Cannot find gateway for ip %s - %q", ip.String(), err)
		return false
	}
	logrus.Debugf("Routes for ip %s: %v", ip.String(), routes)
	for _, r := range routes {
		if r.Gw.Equal(via) {
			return true
		}
	}
	return false
}

func setupRoute(ip string, via net.IP) error {
	_, dst, err := net.ParseCIDR(ip + "/32")
	if err != nil {
		return err
	}
	route := &netlink.Route{
		Scope: netlink.SCOPE_UNIVERSE,
		Dst:   dst,
		Gw:    via,
	}
	if err := netlink.RouteAdd(route); err != nil {
		return err
	}
	return nil
}

func main() {

	lvl, ok := os.LookupEnv("LOG_LEVEL")
	// LOG_LEVEL not set, let's default to debug
	if !ok {
		lvl = "info"
	}
	// parse string, this is built-in feature of logrus
	ll, err := logrus.ParseLevel(lvl)
	if err != nil {
		ll = logrus.DebugLevel
	}
	// set global log level
	logrus.SetLevel(ll)

	nasListFile := OptionalEnv("OVH_NASHA_LIST", "/nasha/partitions.json")
	rawNasList, err := ioutil.ReadFile(nasListFile)
	if err != nil {
		logrus.Fatalf("Cannot read %s file: %q", nasListFile, err)
	}

	var partList []NasPartition
	if err := json.Unmarshal(rawNasList, &partList); err != nil {
		logrus.Fatalf("Cannot unmarshal nas list: %q", err)
	}

	routes, err := netlink.RouteGet(net.IPv4(1, 1, 1, 1))
	if err != nil {
		logrus.Fatal("cannot load routing table: ", err)
	}
	publicGatewayIp := routes[0].Gw

	logrus.Debugf("Public gateway ip: %v", publicGatewayIp.String())

	for {
		for _, partition := range partList {
			if isRoutedVia(publicGatewayIp, net.ParseIP(partition.Ip)) {
				logrus.Debugf("%s is already routed via public gateway, nothing to do.", partition.Ip)
			} else {
				err := setupRoute(partition.Ip, publicGatewayIp)
				if err != nil {
					logrus.Errorf("Cannot add route %s to %s - %v", partition.Ip, publicGatewayIp.String(), err)
					continue
				}
				logrus.Infof("Adding route to %s via %s", partition.Ip, publicGatewayIp.String())
			}
		}
		time.Sleep(60 * time.Second)
	}
}
