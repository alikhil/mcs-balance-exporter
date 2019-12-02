package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"go.uber.org/atomic"
)

type mcsSigninResponse struct {
	Ctime     string `json:"ctime"`
	Tfa       bool   `json:"tfa"`
	UID       string `json:"uid"`
	Postpaid  bool   `json:"postpaid"`
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	Projects  []struct {
		Roles     []string `json:"roles"`
		Pid       string   `json:"pid"`
		Protected bool     `json:"protected"`
		Title     string   `json:"title"`
		Enabled   bool     `json:"enabled"`
		IsPartner bool     `json:"is_partner"`
	} `json:"projects"`
	Attr struct {
		Protected struct {
			Mtime int    `json:"mtime"`
			Flag  bool   `json:"flag"`
			Actor string `json:"actor"`
		} `json:"protected"`
	} `json:"attr"`
	Email    string `json:"email"`
	Domain   string `json:"domain"`
	Verified struct {
		Email bool `json:"email"`
		Phone bool `json:"phone"`
	} `json:"verified"`
	Enabled bool `json:"enabled"`
}

type balanceResponse struct {
	Postpaid  bool   `json:"postpaid"`
	Bind      bool   `json:"bind"`
	Bonus     int    `json:"bonus"`
	LegalForm string `json:"legal_form"`
	Currency  string `json:"currency"`
	Domain    string `json:"domain"`
	Autopay   bool   `json:"autopay"`
	Pid       string `json:"pid"`
	Balance   string `json:"balance"`
	Extra     struct {
		Ctime         int  `json:"ctime"`
		RegNotified   bool `json:"reg_notified"`
		FirstPayDmr   int  `json:"first_pay_dmr"`
		FirstPayOrder int  `json:"first_pay_order"`
	} `json:"extra"`
}

type project struct {
	id    string
	title string
}

type mcsContext struct {
	credentials *CredentialsConfig
	sessionKey  string
	projectsIDs []project
	client      *http.Client
	authorized  *atomic.Bool
	authLock    sync.Mutex
	cookies     []*http.Cookie
}

type mcsCredentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func newContext(cfg *CredentialsConfig) *mcsContext {
	// remove this dirty hack
	credentials = *cfg
	return &mcsContext{
		credentials: cfg,
		authorized:  atomic.NewBool(false),
		client: &http.Client{
			Timeout: time.Second * 2,
		},
	}
}

func (ctx *mcsContext) getBalance(projectID string) (float64, error) {
	if !ctx.authorized.Load() {
		ctx.authLock.Lock()
		defer ctx.authLock.Unlock()
		var err = ctx.authorize()
		if err != nil {
			return 0, err
		}
	}

	// curl 'https://mcs.mail.ru/api/v1/projects/mcs3968975066/billing'  -H 'Accept: application/json, text/javascript, */*; q=0.01'  -H 'Cookie: sid=jh6GNCV5J3A5VDce9GQYZ9; '
	var url = fmt.Sprintf("https://mcs.mail.ru/api/v1/projects/%s/billing", projectID)
	var req, err = http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, newError("request creation error: %v", err)
	}

	addMCSHeaders(req)

	for _, cookie := range ctx.cookies {
		req.AddCookie(cookie)
	}

	resp, err := ctx.client.Do(req)
	if err != nil {
		return 0, newError("response error: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, newError("response status code is not 200, but - %v", resp.StatusCode)
	}

	rbody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, newError("read response body error: %v", err)
	}

	var balanceResp = &balanceResponse{}
	err = json.Unmarshal(rbody, balanceResp)

	if err != nil {
		return 0, newError("unmarshall error: %v", err)
	}

	return strconv.ParseFloat(balanceResp.Balance, 64)
}

func (ctx *mcsContext) authorize() error {
	if ctx.authorized.Load() {
		return nil
	}

	var body = &bytes.Buffer{}

	var payload, merror = json.Marshal(mcsCredentials{Email: ctx.credentials.Login, Password: ctx.credentials.Password})
	if merror != nil {
		return newError("marshal error: %v", merror)
	}
	body.Write(payload)

	var req, err = http.NewRequest(http.MethodPost, "https://mcs.mail.ru/api/v1/auth/signin", body)
	if err != nil {
		return newError("request creation error: %v", merror)
	}

	addMCSHeaders(req)
	res, err := ctx.client.Do(req)
	if err != nil {
		return newError("response error: %v", err)
	}

	if res.StatusCode != http.StatusOK {
		return newError("auth response status code is not 200, but - %v", res.StatusCode)
	}

	rbody, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return newError("read response body error: %v", err)
	}

	var resp = &mcsSigninResponse{}
	log.Printf("%s", rbody)
	err = json.Unmarshal(rbody, resp)

	if err != nil {
		return newError("unmarshall error: %v", err)
	}

	ctx.cookies = res.Cookies()
	var projectIDs = make([]project, len(resp.Projects))
	for i, p := range resp.Projects {
		projectIDs[i] = project{id: p.Pid, title: p.Title}
	}
	ctx.projectsIDs = projectIDs
	ctx.authorized.Store(true)
	return nil
}

func addMCSHeaders(r *http.Request) {
	r.Header.Add("Accept", "application/json, text/javascript, */*; q=0.01")
	r.Header.Add("Content-Type", "application/json")
}
