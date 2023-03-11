package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/ovh/go-ovh/ovh"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Params
type AccessPosttParams struct {
	Ip         string `json:"ip"`
	AccessType string `json:"type"`
}

type AccessIP struct {
	Ip         string `json:"ip"`
	AccessType string `json:"type"`
	AccessId   int    `json:"accessId"`
}

type NasPartition struct {
	Ip    string `json:"ip"`
	Name  string `json:"name"`
	NasHa string `json:"nasha"`
}

func RequiredEnv(envName string) string {
	env, isSet := os.LookupEnv(envName)
	if !isSet || len(env) == 0 {
		logrus.Fatal("Error: Required env var ", envName, " is missing.")
	}
	return env
}

func OptionalEnv(envName string, defaultValue string) string {
	env, isSet := os.LookupEnv(envName)
	if !isSet || len(env) == 0 {
		return defaultValue
	}
	return env
}

func isNasPartitionAccessExists(client *ovh.Client, part NasPartition, ip string) bool {
	var ipAccess AccessIP
	if err := client.Get(fmt.Sprintf("/dedicated/nasha/%s/partition/%s/access/%s", part.NasHa, part.Name, ip), &ipAccess); err != nil {
		logrus.Debugf("Error getting %s nasha ip access on partition %s for ip %s - %v", part.NasHa, part.Name, ip, err)
		return false
	}
	return ipAccess != (AccessIP{})

}

func GetNasAccessesCidrsForPartition(client *ovh.Client, part NasPartition) []string {
	var ips []string
	if err := client.Get(fmt.Sprintf("/dedicated/nasha/%s/partition/%s/access", part.NasHa, part.Name), &ips); err != nil {
		logrus.Debugf("Error getting %s nasha ip accesses on partition %s  - %v", part.NasHa, part.Name, err)
		return nil
	}
	return ips
}

func GetK8sNodesExternalIps(c *kubernetes.Clientset) []string {
	nodes, err := c.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}

	var publicIps []string
	for _, n := range nodes.Items {
		for _, a := range n.Status.Addresses {
			if a.Type == v1.NodeExternalIP {
				publicIps = append(publicIps, a.Address)
				break
			}
		}
	}
	return publicIps
}

func isStringInList(l []string, n string) bool {
	for _, s := range l {
		if s == n {
			return true
		}
	}
	return false
}

func isStringInAccessList(l []AccessIP, n string) bool {
	for _, a := range l {
		if a.Ip == n {
			return true
		}
	}
	return false
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

	client, err := ovh.NewEndpointClient("ovh-eu")
	if err != nil {
		logrus.Fatalf("Error connecting to OVH API: %q", err)
	}

	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	for {
		nodesExternalIps := GetK8sNodesExternalIps(clientset)
		for _, partition := range partList {
			partitionAccessesCidrs := GetNasAccessesCidrsForPartition(client, partition)
			logrus.Debugf("%s/%s access list %v.", partition.NasHa, partition.Name, partitionAccessesCidrs)
			for _, cidr := range partitionAccessesCidrs {
				ip := strings.Split(cidr, "/")[0]
				if !isStringInList(nodesExternalIps, ip) {
					// node no longer exists, delete access
					if err := client.Delete(fmt.Sprintf("/dedicated/nasha/%s/partition/%s/access/%s", partition.NasHa, partition.Name, ip), nil); err != nil {
						logrus.Errorf("Error deleting access to ip %s on %s/%s nasha - %v", ip, partition.NasHa, partition.Name, err)
					}
					logrus.Infof("%s access on %s/%s nasha deleted.", ip, partition.NasHa, partition.Name)
				}
			}
			for _, nodeIp := range nodesExternalIps {
				if !isStringInList(partitionAccessesCidrs, nodeIp+"/32") {
					// node access missing, let's add it.
					params := &AccessPosttParams{Ip: nodeIp, AccessType: "readwrite"}
					if err := client.Post(fmt.Sprintf("/dedicated/nasha/%s/partition/%s/access", partition.NasHa, partition.Name), &params, nil); err != nil {
						logrus.Errorf("Error addind access to ip %s on %s/%s nasha - %v", nodeIp, partition, err)
					}
					logrus.Infof("%s access on %s/%s nasha granted.", nodeIp, partition.NasHa, partition.Name)
				}
			}
		}
		time.Sleep(60 * time.Second)
	}
}
