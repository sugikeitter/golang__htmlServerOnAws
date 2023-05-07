package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

//go:embed template
var f embed.FS
var tmpl, _ = template.ParseFS(f, "template/index.html")
var jst, _ = time.LoadLocation("Asia/Tokyo")
var h3Color string
var privateIps string
var awsAz string
var counter = 0

type Result struct {
	Time       string
	Counter    int
	Name       string
	PrivateIps string
	AwsAz      string
	H3Color    string
}

type EcsTaskMeta struct {
	AvailabilityZone string `json:"AvailabilityZone"`
}

func handler(w http.ResponseWriter, r *http.Request) {
	counter++
	fmt.Println(currentTime(), "Count:", counter, "IP:", r.RemoteAddr)
	result := Result{
		Time:       currentTime(),
		Counter:    counter,
		Name:       r.FormValue("name"),
		PrivateIps: myPrivateIps(),
		AwsAz:      awsAzFromMetadata(),
		H3Color:    h3Color,
	}
	tmpl.Execute(w, result)
}

func handleIcon(w http.ResponseWriter, r *http.Request) {}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "OK")
}

// TODO logger
func main() {
	usageMsg := `Usage:
	<command> <port>`

	if len(os.Args) != 2 {
		fmt.Println(usageMsg)
		os.Exit(1)
	}

	// 引数が数値じゃない場合など、デフォルトのポートは8080
	port := os.Args[1]
	_, err := strconv.Atoi(port)
	if err != nil {
		port = "8080"
	}
	// 色を環境変数から取得
	val, ok := os.LookupEnv("H3_COLOR")
	if !ok {
		h3Color = "33, 119, 218" // Default Blue
		//h3Color = "63, 177, 12" // Default Gleen
		//h3Color = "248, 52, 0" // Default Red
	} else {
		h3Color = val
	}
	// TODO template に文字を書ける場所を用意

	go myPrivateIps()
	go awsAzFromMetadata()

	http.HandleFunc("/", handler)
	http.HandleFunc("/favicon.ico", handleIcon)
	http.HandleFunc("/health", handleHealth)
	fmt.Println(currentTime(), "start!!")
	http.ListenAndServe(":"+port, nil)
}

func currentTime() string {
	t := time.Now().In(jst)
	return t.Format("2006/01/02 15:04:05.000")
}

func myPrivateIps() string {
	if privateIps != "" {
		return privateIps
	}
	netInterfaceAddresses, _ := net.InterfaceAddrs()

	var ips []string
	for _, netInterfaceAddress := range netInterfaceAddresses {
		networkIp, ok := netInterfaceAddress.(*net.IPNet)
		if ok && !networkIp.IP.IsLoopback() && networkIp.IP.To4() != nil {
			ips = append(ips, networkIp.IP.String())
		}
	}
	privateIps = fmt.Sprintf("%s", ips)
	return privateIps
}

// TODO IMDS v2
func awsAzFromMetadata() string {
	if awsAz != "" {
		return awsAz
	}
	// TODO client.Get/client.Do(req) などは 404 の場合は err に値が入るのか確認
	client := http.Client{
		Timeout: 1 * time.Second,
	}
	// ECS のメタデータから取得できれば利用する
	if az, _ := awsAzFromEcsMeta(client); az != "" {
		awsAz = az
		return az
	}
	// TODO EKS の場合？

	// EC2 インスタンス
	if az, _ := awsAzFromEc2MetaV1(client); az != "" {
		awsAz = az
		return az
	}
	// IMDS v2 Only で設定されている場合 err はなく空文字が返る
	az, _ := awsAzFromEc2MetaV2(client)
	awsAz = az
	return az
}

func awsAzFromEc2MetaV1(client http.Client) (string, error) {
	resp, err := client.Get("http://169.254.169.254/latest/meta-data/placement/availability-zone")
	if err != nil {
		// log
		fmt.Println(currentTime(), "ERROR - http.get from IMDSv1")
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(currentTime(), "ERROR - io.ReadAll from IMDSv1")
		return "", err
	}
	return string(body[:]), nil
}

func awsAzFromEc2MetaV2(client http.Client) (string, error) {
	tokenReq, err := http.NewRequest("PUT", "http://169.254.169.254/latest/api/token", nil)
	tokenReq.Header.Add("X-aws-ec2-metadata-token-ttl-seconds", "120")
	tokenResp, err := client.Do(tokenReq)
	defer tokenResp.Body.Close()
	tokenRespBody, err := io.ReadAll(tokenResp.Body)
	if err != nil {
		fmt.Println(currentTime(), "ERROR - io.ReadAll from IMDSv2 token")
		return "", err
	}
	imdsV2Token := string(tokenRespBody[:])

	// IMDS v2 で AZ 取得
	req, err := http.NewRequest("GET", "http://169.254.169.254/latest/meta-data/placement/availability-zone", nil)
	req.Header.Add("X-aws-ec2-metadata-token", imdsV2Token)
	resp, err := client.Do(req)
	if err != nil {
		// log
		fmt.Println(currentTime(), "ERROR - http.get from IMDSv2")
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(currentTime(), "ERROR - io.ReadAll from IMDS")
		return "", err
	}
	return string(body[:]), nil
}

func awsAzFromEcsMeta(client http.Client) (string, error) {
	resp, err := client.Get(os.Getenv("ECS_CONTAINER_METADATA_URI_V4") + "/task")
	if err != nil {
		// log
		fmt.Println(currentTime(), "ERROR - http.get from ECS_CONTAINER_METADATA_URI_V4")
		return "", err
	}
	defer resp.Body.Close()
	var ecsTaskMeta EcsTaskMeta
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&ecsTaskMeta); err != nil {
		// log
		fmt.Println(currentTime(), "ERROR - Decode ECS_CONTAINER_METADATA_URI_V4")
		return "", err
	}
	return ecsTaskMeta.AvailabilityZone, nil
}
