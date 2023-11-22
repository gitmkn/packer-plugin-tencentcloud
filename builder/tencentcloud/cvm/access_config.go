// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:generate packer-sdc struct-markdown

package cvm

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"runtime"
	"strings"

	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
	"github.com/mitchellh/go-homedir"
	cvm "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cvm/v20170312"
	vpc "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/vpc/v20170312"
)

const (
	PACKER_SECRET_ID                    = "TENCENTCLOUD_SECRET_ID"
	PACKER_SECRET_KEY                   = "TENCENTCLOUD_SECRET_KEY"
	PACKER_SECURITY_TOKEN               = "TENCENTCLOUD_SECURITY_TOKEN"
	PACKER_REGION                       = "TENCENTCLOUD_REGION"
	PACKER_ASSUME_ROLE_ARN              = "TENCENTCLOUD_ASSUME_ROLE_ARN"
	PACKER_ASSUME_ROLE_SESSION_NAME     = "TENCENTCLOUD_ASSUME_ROLE_SESSION_NAME"
	PACKER_ASSUME_ROLE_SESSION_DURATION = "TENCENTCLOUD_ASSUME_ROLE_SESSION_DURATION"
	PACKER_PROFILE                      = "TENCENTCLOUD_PROFILE"
	PACKER_SHARED_CREDENTIALS_DIR       = "TENCENTCLOUD_SHARED_CREDENTIALS_DIR"
	DEFAULT_REGION                      = "ap-guangzhou"
	DEFAULT_PROFILE                     = "default"
)

type Region string

// below would be moved to tencentcloud sdk git repo
const (
	Bangkok       = Region("ap-bangkok")
	Beijing       = Region("ap-beijing")
	Chengdu       = Region("ap-chengdu")
	Chongqing     = Region("ap-chongqing")
	Guangzhou     = Region("ap-guangzhou")
	GuangzhouOpen = Region("ap-guangzhou-open")
	Hongkong      = Region("ap-hongkong")
	Jakarta       = Region("ap-jakarta")
	Mumbai        = Region("ap-mumbai")
	Seoul         = Region("ap-seoul")
	Shanghai      = Region("ap-shanghai")
	Nanjing       = Region("ap-nanjing")
	ShanghaiFsi   = Region("ap-shanghai-fsi")
	ShenzhenFsi   = Region("ap-shenzhen-fsi")
	Singapore     = Region("ap-singapore")
	Tokyo         = Region("ap-tokyo")
	Frankfurt     = Region("eu-frankfurt")
	Moscow        = Region("eu-moscow")
	Ashburn       = Region("na-ashburn")
	Siliconvalley = Region("na-siliconvalley")
	Toronto       = Region("na-toronto")
	SaoPaulo      = Region("sa-saopaulo")
)

var ValidRegions = []Region{
	Bangkok, Beijing, Chengdu, Chongqing, Guangzhou, GuangzhouOpen, Hongkong, Jakarta, Shanghai, Nanjing,
	ShanghaiFsi, ShenzhenFsi,
	Mumbai, Seoul, Singapore, Tokyo, Moscow,
	Frankfurt, Ashburn, Siliconvalley, Toronto, SaoPaulo,
}

var packerConfig map[string]interface{}

type TencentCloudAccessConfig struct {
	// Tencentcloud secret id. You should set it directly,
	// or set the TENCENTCLOUD_SECRET_ID environment variable.
	SecretId string `mapstructure:"secret_id" required:"true"`
	// Tencentcloud secret key. You should set it directly,
	// or set the TENCENTCLOUD_SECRET_KEY environment variable.
	SecretKey string `mapstructure:"secret_key" required:"true"`
	// The region where your cvm will be launch. You should
	// reference Region and Zone
	//  for parameter taking.
	Region string `mapstructure:"region" required:"true"`
	// The zone where your cvm will be launch. You should
	// reference Region and Zone
	//  for parameter taking.
	Zone string `mapstructure:"zone" required:"true"`
	// The endpoint you want to reach the cloud endpoint,
	// if tce cloud you should set a tce cvm endpoint.
	CvmEndpoint string `mapstructure:"cvm_endpoint" required:"false"`
	// The endpoint you want to reach the cloud endpoint,
	// if tce cloud you should set a tce vpc endpoint.
	VpcEndpoint string `mapstructure:"vpc_endpoint" required:"false"`
	// The region validation can be skipped if this value is true, the default
	// value is false.
	skipValidation bool
	// STS access token, can be set through template or by exporting
	// as environment variable such as `export SECURITY_TOKEN=value`.
	SecurityToken string `mapstructure:"security_token" required:"false"`
	// The ARN of the role to assume.
	// It can be sourced from the `TENCENTCLOUD_ASSUME_ROLE_ARN`.
	RoleArn string `mapstructure:"role_arn" required:"false"`
	// The session name to use when making the AssumeRole call.
	// It can be sourced from the `TENCENTCLOUD_ASSUME_ROLE_SESSION_NAME`.
	SessionName string `mapstructure:"session_name" required:"false"`
	// The duration of the session when making the AssumeRole call.
	// Its value ranges from 0 to 43200(seconds), and default is 7200 seconds.
	// It can be sourced from the `TENCENTCLOUD_ASSUME_ROLE_SESSION_DURATION`.",
	SessionDuration int `mapstructure:"session_duration" required:"false"`
	// The directory of the shared credentials.
	// It can also be sourced from the `TENCENTCLOUD_SHARED_CREDENTIALS_DIR` environment variable.
	// If not set this defaults to ~/.tccli.
	Profile string
	// The profile name as set in the shared credentials.
	// It can also be sourced from the `TENCENTCLOUD_PROFILE` environment variable.
	// If not set, the default profile created with `tccli configure` will be used.
	SharedCredentialsDir string
}

func (cf *TencentCloudAccessConfig) Client() (*cvm.Client, *vpc.Client, error) {
	var (
		err        error
		cvm_client *cvm.Client
		vpc_client *vpc.Client
		resp       *cvm.DescribeZonesResponse
	)

	var getProviderConfig = func(str string, key string) string {
		value, err := getConfigFromProfile(cf, key)
		if err == nil && value != nil {
			str = value.(string)
		}
		return str
	}

	if cf.SecretId == "" {
		cf.SecretId = getProviderConfig(cf.SecretId, "secret_id")
	}
	if cf.SecretKey == "" {
		cf.SecretKey = getProviderConfig(cf.SecretKey, "secret_key")
	}
	if cf.SecurityToken == "" {
		cf.SecurityToken = getProviderConfig(cf.SecurityToken, "security_token")
	}
	if cf.Region == "" {
		cf.Region = getProviderConfig(cf.Region, "region")
	}

	if err = cf.validateRegion(); err != nil {
		return nil, nil, err
	}

	if cf.Zone == "" {
		return nil, nil, fmt.Errorf("parameter zone must be set")
	}

	if cvm_client, err = NewCvmClient(cf); err != nil {
		return nil, nil, err
	}

	if vpc_client, err = NewVpcClient(cf); err != nil {
		return nil, nil, err
	}

	ctx := context.TODO()
	err = Retry(ctx, func(ctx context.Context) error {
		var e error
		resp, e = cvm_client.DescribeZones(nil)
		return e
	})
	if err != nil {
		return nil, nil, err
	}

	for _, zone := range resp.Response.ZoneSet {
		if cf.Zone == *zone.Zone {
			return cvm_client, vpc_client, nil
		}
	}

	return nil, nil, fmt.Errorf("unknown zone: %s", cf.Zone)
}

func (cf *TencentCloudAccessConfig) Prepare(ctx *interpolate.Context) []error {
	var errs []error

	if err := cf.Config(); err != nil {
		errs = append(errs, err)
	}

	if (cf.CvmEndpoint != "" && cf.VpcEndpoint == "") ||
		(cf.CvmEndpoint == "" && cf.VpcEndpoint != "") {
		errs = append(errs, fmt.Errorf("parameter cvm_endpoint and vpc_endpoint must be set simultaneously"))
	}

	if cf.Region == "" {
		errs = append(errs, fmt.Errorf("parameter region must be set"))
	} else if !cf.skipValidation {
		if err := cf.validateRegion(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

func (cf *TencentCloudAccessConfig) Config() error {
	if cf.SecretId == "" {
		cf.SecretId = os.Getenv(PACKER_SECRET_ID)
	}

	if cf.SecretKey == "" {
		cf.SecretKey = os.Getenv(PACKER_SECRET_KEY)
	}

	if cf.Profile == "" {
		cf.Profile = os.Getenv(PACKER_PROFILE)
	}

	if cf.SharedCredentialsDir == "" {
		cf.SharedCredentialsDir = os.Getenv(PACKER_SHARED_CREDENTIALS_DIR)
	}

	if (cf.SecretId == "" || cf.SecretKey == "") && cf.Profile == "" {
		return fmt.Errorf("The parameter secret_id and secret_key must be set or a profile must be set.")
	}

	return nil
}

func (cf *TencentCloudAccessConfig) validateRegion() error {
	// if set cvm endpoint, do not validate region
	if cf.CvmEndpoint != "" {
		return nil
	}
	return validRegion(cf.Region)
}

func validRegion(region string) error {
	for _, valid := range ValidRegions {
		if Region(region) == valid {
			return nil
		}
	}

	return fmt.Errorf("unknown region: %s", region)
}

func getConfigFromProfile(cf *TencentCloudAccessConfig, ProfileKey string) (interface{}, error) {
	var (
		profile              string
		sharedCredentialsDir string
		credentialPath       string
		configurePath        string
	)

	if cf.Profile != "" {
		profile = cf.Profile
	} else {
		profile = DEFAULT_PROFILE
	}

	if cf.SharedCredentialsDir != "" {
		sharedCredentialsDir = cf.SharedCredentialsDir
	}

	tmpSharedCredentialsDir, err := homedir.Expand(sharedCredentialsDir)
	if err != nil {
		return nil, err
	}

	if tmpSharedCredentialsDir == "" {
		credentialPath = fmt.Sprintf("%s/.tccli/%s.credential", os.Getenv("HOME"), profile)
		configurePath = fmt.Sprintf("%s/.tccli/%s.configure", os.Getenv("HOME"), profile)
		if runtime.GOOS == "windows" {
			credentialPath = fmt.Sprintf("%s/.tccli/%s.credential", os.Getenv("USERPROFILE"), profile)
			configurePath = fmt.Sprintf("%s/.tccli/%s.configure", os.Getenv("USERPROFILE"), profile)
		}
	} else {
		credentialPath = fmt.Sprintf("%s/%s.credential", sharedCredentialsDir, profile)
		configurePath = fmt.Sprintf("%s/%s.configure", sharedCredentialsDir, profile)
	}

	packerConfig = make(map[string]interface{})
	_, err = os.Stat(credentialPath)
	if !os.IsNotExist(err) {
		data, err := ioutil.ReadFile(credentialPath)
		if err != nil {
			return nil, err
		}

		config := map[string]interface{}{}
		err = json.Unmarshal(data, &config)
		if err != nil {
			return nil, err
		}

		for k, v := range config {
			packerConfig[k] = strings.TrimSpace(v.(string))
		}
	}
	_, err = os.Stat(configurePath)
	if !os.IsNotExist(err) {
		data, err := ioutil.ReadFile(configurePath)
		if err != nil {
			return nil, err
		}

		config := map[string]interface{}{}
		err = json.Unmarshal(data, &config)
		if err != nil {
			return nil, err
		}

	outerLoop:
		for k, v := range config {
			if k == "_sys_param" {
				tmpMap := v.(map[string]interface{})
				for tmpK, tmpV := range tmpMap {
					if tmpK == "region" {
						packerConfig[tmpK] = strings.TrimSpace(tmpV.(string))
						break outerLoop
					}
				}
			}
		}
	}

	return packerConfig[ProfileKey], nil
}
