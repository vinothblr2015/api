// Copyright (c) 2019 Huawei Technologies Co., Ltd. All Rights Reserved.
//
//    Licensed under the Apache License, Version 2.0 (the "License"); you may
//    not use this file except in compliance with the License. You may obtain
//    a copy of the License at
//
//         http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
//    WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
//    License for the specific language governing permissions and limitations
//    under the License.

package fusionstorage

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	log "github.com/golang/glog"
	pb "github.com/opensds/opensds/pkg/dock/proto"
	"github.com/opensds/opensds/pkg/utils/exec"
)

var CliErrorMap = map[string]string{
	"50000001": "DSware error",
	"50150001": "Receive a duplicate request",
	"50150002": "Command type is not supported",
	"50150003": "Command format is error",
	"50150004": "Lost contact with major VBS",
	"50150005": "Volume does not exist",
	"50150006": "Snapshot does not exist",
	"50150007": "Volume already exists or name exists or name duplicates with a snapshot name",
	"50150008": "The snapshot has already existed",
	"50150009": "VBS space is not enough",
	"50150010": "The node type is error",
	"50150011": "Volume and snapshot number is beyond max",
	"50150012": "VBS is not ready",
	"50150013": "The ref num of node is not 0",
	"50150014": "The volume is not in the pre-deletion state.",
	"50150015": "The storage resource pool is faulty",
	"50150016": "VBS handle queue busy",
	"50150017": "VBS handle request timeout",
	"50150020": "VBS metablock is locked",
	"50150021": "VBS pool dose not exist",
	"50150022": "VBS is not ok",
	"50150023": "VBS pool is not ok",
	"50150024": "VBS dose not exist",
	"50150064": "VBS load SCSI-3 lock pr meta failed",
	"50150100": "The disaster recovery relationship exists",
	"50150101": "The DR relationship does not exist",
	"50150102": "Volume has existed mirror",
	"50150103": "The volume does not have a mirror",
	"50150104": "Incorrect volume status",
	"50150105": "The mirror volume already exists",
}

func NewCliError(code string) error {
	if msg, ok := CliErrorMap[code]; ok {
		return NewCliErrorBase(msg, code)
	}
	return NewCliErrorBase("CLI execute error", code)
}

type CliError struct {
	Msg  string
	Code string
}

func (c *CliError) Error() string {
	return fmt.Sprintf("msg: %s, code:%s", c.Msg, c.Code)
}

func NewCliErrorBase(msg, code string) *CliError {
	return &CliError{Msg: msg, Code: code}
}

type Cli struct {
	username string
	password string
	version  string
	addess   string
	headers  map[string]string
	// Command Root exectuer
	rootExecuter exec.Executer
	fmIp         string
	fsaIp        []string
}

func newRestCommon(username, password, url, fmIp string, fsaIP []string) (*Cli, error) {
	if len(fmIp) == 0 || len(fsaIP) == 0 {
		return nil, fmt.Errorf("new cli failed, FM ip or FSA ip can not be set to empty")
	}

	return &Cli{
		addess:       url,
		username:     username,
		password:     password,
		rootExecuter: exec.NewRootExecuter(),
		fmIp:         fmIp,
		fsaIp:        fsaIP,
		headers:      map[string]string{"Content-Type": "application/json;charset=UTF-8"},
	}, nil
}

func (c *Cli) getVersion() error {
	url := "rest/version"
	c.headers["Referer"] = c.addess + BasicURI
	content, err := c.request(url, "GET", true, nil)
	if err != nil {
		return fmt.Errorf("Failed to get version, %v", err)
	}

	var v version
	err = json.Unmarshal(content, &v)
	if err != nil {
		return fmt.Errorf("Failed to unmarshal the result, %v", err)
	}

	c.version = v.CurrentVersion

	return nil
}

func (c *Cli) login() error {
	c.getVersion()
	url := "/sec/login"
	data := map[string]string{"userName": c.username, "password": c.password}
	_, err := c.request(url, "POST", false, data)
	if err != nil {
		return err
	}

	return nil
}

func (c *Cli) queryPoolInfo() (*poolResp, error) {
	url := "/storagePool"
	result, err := c.request(url, "GET", false, nil)
	if err != nil {
		return nil, err
	}

	var pools *poolResp
	if err := json.Unmarshal(result, &pools); err != nil {
		return nil, err
	}
	return pools, nil
}

func (c *Cli) createVolume(volName, poolId string, volSize int64) error {
	url := "/volume/create"
	polID, _ := strconv.Atoi(poolId)
	params := map[string]interface{}{"volName": volName, "volSize": volSize, "poolId": polID}
	if _, err := c.request(url, "POST", false, params); err != nil {
		return err
	}
	return nil
}

func (c *Cli) deleteVolume(volName string) error {
	url := "/volume/delete"
	params := map[string]interface{}{"volNames": []string{volName}}
	_, err := c.request(url, "POST", false, params)
	if err != nil {
		return err
	}

	return nil
}

func (c *Cli) attachVolume(volName, manageIp string) error {
	url := "/volume/attach"
	params := map[string]interface{}{"volName": []string{volName}, "ipList": []string{manageIp}}
	result, err := c.request(url, "POST", false, params)
	if err != nil {
		return err
	}
	fmt.Println(string(result))
	return nil
}

func (c *Cli) createPort(initiator string) error {
	url := "iscsi/createPort"
	params := map[string]interface{}{"portName": initiator}
	_, err := c.request(url, "POST", true, params)
	if err != nil {
		return err
	}
	return nil
}

func (c *Cli) queryPortInfo(initiator string) error {
	url := "iscsi/queryPortInfo"
	params := map[string]interface{}{"portName": initiator}
	_, err := c.request(url, "POST", true, params)
	if err != nil {
		return err
	}

	return nil
}

func (c *Cli) queryHostInfo(hostName string) (bool, error) {
	url := "iscsi/queryAllHost"
	result, err := c.request(url, "GET", true, nil)
	if err != nil {
		return false, err
	}

	var hostlist *hostList

	if err := json.Unmarshal(result, &hostlist); err != nil {
		return false, err
	}

	for _, v := range hostlist.HostList {
		if v.HostName == hostName {
			return true, nil
		}
	}

	return false, nil
}

func (c *Cli) createHost(hostInfo *pb.HostInfo) error {
	url := "iscsi/createHost"
	params := map[string]interface{}{"hostName": hostInfo.GetHost(), "ipAddress": hostInfo.GetIp()}
	_, err := c.request(url, "POST", true, params)
	if err != nil {
		return err
	}
	return nil
}

func (c *Cli) addPortToHost(hostName, initiator string) error {
	url := "iscsi/addPortToHost"
	params := map[string]interface{}{"hostName": hostName, "portNames": []string{initiator}}
	_, err := c.request(url, "POST", true, params)
	if err != nil {
		return err
	}
	return nil
}

func (c *Cli) queryHostByPort(initiator string) (*portHostMap, error) {
	url := "iscsi/queryHostByPort"
	params := map[string]interface{}{"portName": []string{initiator}}
	result, err := c.request(url, "POST", true, params)
	if err != nil {
		return nil, err
	}

	var portHostmap *portHostMap

	if err := json.Unmarshal(result, &portHostmap); err != nil {
		return nil, err
	}

	return portHostmap, nil
}

func (c *Cli) addLunsToHost(hostName, lunId string) error {
	url := "iscsi/addLunsToHost"
	params := map[string]interface{}{"hostName": hostName, "lunNames": []string{lunId}}
	_, err := c.request(url, "POST", true, params)
	if err != nil {
		return err
	}
	return nil
}

func (c *Cli) queryHostLunInfo(hostName string) (*hostLunList, error) {
	url := "iscsi/queryHostLunInfo"
	params := map[string]interface{}{"hostName": hostName}
	result, err := c.request(url, "POST", true, params)
	if err != nil {
		return nil, err
	}

	var lunList *hostLunList

	if err := json.Unmarshal(result, &lunList); err != nil {
		return nil, err
	}

	return lunList, nil
}

func (c *Cli) queryIscsiPortal(initiator string) (string, error) {
	args := []string{
		"--op", "queryIscsiPortalInfo", "--portName", initiator,
	}
	out, err := c.RunCmd(args...)
	if err != nil {
		log.Errorf("Query iscsi portal failed: %v", err)
		return "", err
	}

	if len(out) > 0 {
		return out[0], nil
	}

	return "", fmt.Errorf("The iscsi target portal is empty.")
}

func (c *Cli) queryHostFromVolume(lunId string) ([]host, error) {
	url := "iscsi/queryHostFromVolume"
	params := map[string]interface{}{"lunName": lunId}
	out, err := c.request(url, "POST", true, params)
	if err != nil {
		return nil, err
	}

	var hostlist *hostList

	if err := json.Unmarshal(out, &hostlist); err != nil {
		return nil, err
	}

	return hostlist.HostList, nil
}

func (c *Cli) deleteLunFromHost(hostName, lunId string) error {
	url := "iscsi/deleteLunFromHost"
	params := map[string]interface{}{"hostName": hostName, "lunNames": []string{lunId}}
	_, err := c.request(url, "POST", true, params)
	if err != nil {
		return err
	}
	return nil
}

func (c *Cli) deletePortFromHost(hostName, initiator string) error {
	url := "iscsi/deletePortFromHost"
	params := map[string]interface{}{"hostName": hostName, "portNames": []string{initiator}}
	_, err := c.request(url, "POST", true, params)
	if err != nil {
		return err
	}
	return nil
}

func (c *Cli) deleteHost(hostName string) error {
	url := "iscsi/deleteHost"
	params := map[string]interface{}{"hostName": hostName}
	_, err := c.request(url, "POST", true, params)
	if err != nil {
		return err
	}
	return nil
}

func (c *Cli) deletePort(initiator string) error {
	url := "iscsi/deletePort"
	params := map[string]interface{}{"portName": initiator}
	_, err := c.request(url, "POST", true, params)
	if err != nil {
		return err
	}
	return nil
}

func (c *Cli) request(url, method string, isGetVersion bool, reqParams interface{}) ([]byte, error) {
	var callUrl string
	if !isGetVersion {
		callUrl = c.addess + BasicURI + c.version + url
	} else {
		callUrl = c.addess + BasicURI + url
	}

	fmt.Println(callUrl)
	// No verify by SSL
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	// initialize http client
	client := &http.Client{Transport: tr}

	var body []byte
	var err error
	if reqParams != nil {
		body, err = json.Marshal(reqParams)
		if err != nil {
			return nil, fmt.Errorf("Failed to marshal the request parameters, url is %s, error is %v", callUrl, err)
		}
	}

	req, err := http.NewRequest(strings.ToUpper(method), callUrl, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("Failed to initiate the request, url is %s, error is %v", callUrl, err)
	}

	// initiate the header
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	// do the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Process request failed: %v, url is %s", err, callUrl)
	}
	defer resp.Body.Close()

	respContent, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Read from response body failed: %v, url is %s", err, callUrl)
	}

	fmt.Println(string(respContent))
	if 400 <= resp.StatusCode && resp.StatusCode <= 599 {
		pc, _, line, _ := runtime.Caller(1)
		return nil, fmt.Errorf("return status code is: %s, return content is: %s, error function is: %s, error line is: %s, url is %s",
			strconv.Itoa(resp.StatusCode), string(respContent), runtime.FuncForPC(pc).Name(), strconv.Itoa(line), callUrl)
	}

	// Check the error code in the returned content
	var respResult *responseResult
	if err := json.Unmarshal(respContent, &respResult); err != nil {
		return nil, err
	}

	if respResult.RespCode != 0 {
		return nil, fmt.Errorf(string(respContent))
	}

	if resp.Header != nil && len(resp.Header["X-Auth-Token"]) > 0 {
		token := resp.Header["X-Auth-Token"][0]
		c.headers["x-auth-token"] = token
	}

	return respContent, nil
}

func (c *Cli) StartServer() error {
	_, err := c.rootExecuter.Run(CmdBin, "--op", "startServer")
	if err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	fmt.Println("FSC CLI server start successfully")
	return nil
}

func (c *Cli) RunCmd(args ...string) ([]string, error) {
	var lines []string
	var result string

	args = append(args, "--manage_ip", c.fmIp, "--ip", "")
	for _, ip := range c.fsaIp {
		args[len(args)-1] = ip
		out, _ := c.rootExecuter.Run(CmdBin, args...)
		lines = strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) > 0 {
			const resultPrefix = "result="
			for _, line := range lines {
				if strings.HasPrefix(line, resultPrefix) {
					result = line[len(resultPrefix):]
				}
			}
			if result == "0" {
				return lines[:len(lines)-1], nil
			}
		}
	}

	return nil, NewCliError(result)
}

func (c *Cli) extendVolume(name string, newSize int64) error {
	url := "/volume/expand"
	params := map[string]interface{}{"volName": name, "newVolSize": newSize}
	_, err := c.request(url, "POST", false, params)
	if err != nil {
		return err
	}
	return nil
}

func (c *Cli) createSnapshot(snapName, volName string) error {
	url := "/snapshot/create"
	params := map[string]interface{}{"volName": volName, "snapshotName": snapName}
	_, err := c.request(url, "POST", false, params)
	if err != nil {
		return err
	}
	return nil
}

func (c *Cli) deleteSnapshot(snapName string) error {
	url := "/snapshot/delete"
	params := map[string]interface{}{"snapshotName": snapName}
	_, err := c.request(url, "POST", false, params)
	if err != nil {
		return err
	}
	return nil
}
