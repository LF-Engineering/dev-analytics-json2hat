package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LF-Engineering/dev-analytics-metrics/errs"
	"github.com/LF-Engineering/ssaw/ssawsync"
	_ "github.com/go-sql-driver/mysql"
	yaml "gopkg.in/yaml.v2"
)

const cOrigin = "json2hat"

// Native - keeps fixture slug
type native struct {
	Slug string `yaml:"slug"`
}

// fixtureData - we only need to parse CNCF project slug
type fixtureData struct {
	Native native `yaml:"native"`
}

// gitHubUsers - list of GitHub user data from cncf/devstats.
type gitHubUsers []gitHubUser

// gitHubUser - single GitHug user entry from cncf/devstats `github_users.json` JSON.
type gitHubUser struct {
	Login       string   `json:"login"`
	Email       string   `json:"email"`
	Affiliation string   `json:"affiliation"`
	Name        string   `json:"name"`
	CountryID   *string  `json:"country_id"`
	Sex         *string  `json:"sex"`
	Tz          *string  `json:"tz"`
	SexProb     *float64 `json:"sex_prob"`
}

// affData - holds single affiliation data
type affData struct {
	uuid    string
	company string
	from    time.Time
	to      time.Time
}

// AllAcquisitions contain all company acquisitions data
// Acquisition contains acquired company name regular expression and new company name for it.
type allAcquisitions struct {
	Acquisitions [][2]string `yaml:"acquisitions"`
}

// stringSet - set of strings
type stringSet map[string]struct{}

func fatalOnError(err error) {
	if err != nil {
		tm := time.Now()
		fmt.Printf("Error(time=%+v):\nError: '%s'\nStacktrace:\n%s\n", tm, err.Error(), string(debug.Stack()))
		fmt.Fprintf(os.Stderr, "Error(time=%+v):\nError: '%s'\nStacktrace:\n", tm, err.Error())
		panic("stacktrace")
	}
}

func fatalf(f string, a ...interface{}) {
	fatalOnError(fmt.Errorf(f, a...))
}

// decode emails with ! instead of @
func emailDecode(line string) string {
	re := regexp.MustCompile(`([^\s!]+)!([^\s!]+)`)
	return re.ReplaceAllString(line, `$1@$2`)
}

func timeParseAny(dtStr string) time.Time {
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02 15",
		"2006-01-02",
		"2006-01",
		"2006",
	}
	for _, format := range formats {
		t, e := time.Parse(format, dtStr)
		if e == nil {
			return t
		}
	}
	fatalf("Error:\nCannot parse date: '%v'\n", dtStr)
	return time.Now()
}

func jsonEscape(str string) string {
	b, _ := json.Marshal(str)
	return string(b[1 : len(b)-1])
}

func execCommand(cmdAndArgs []string, env map[string]string) (string, string) {
	command := cmdAndArgs[0]
	arguments := cmdAndArgs[1:]
	//fmt.Printf("%s %+v\n", command, arguments)
	cmd := exec.Command(command, arguments...)
	if len(env) > 0 {
		newEnv := os.Environ()
		for key, value := range env {
			newEnv = append(newEnv, key+"="+value)
		}
		cmd.Env = newEnv
	}
	var (
		stdOut bytes.Buffer
		stdErr bytes.Buffer
	)
	cmd.Stderr = &stdErr
	cmd.Stdout = &stdOut
	fatalOnError(cmd.Start())
	fatalOnError(cmd.Wait())
	return stdOut.String(), stdErr.String()
}

func getUUIDsProjects(es string, slugs []string, uuids map[string]struct{}) (m map[string]map[string]struct{}) {
	m = make(map[string]map[string]struct{})
	fetchSize := 20000
	termsSize := 0xffff
	slug2pattern := make(map[string]string)
	pattern2slug := make(map[string]string)
	for _, slug := range slugs {
		pattern := "sds-" + strings.Replace(slug, "/", "-", -1) + "-git*,-*-for-merge,-*-raw"
		slug2pattern[slug] = pattern
		pattern2slug[pattern] = slug
	}
	thrN := runtime.NumCPU()
	runtime.GOMAXPROCS(thrN)
	//thrN = int(math.Round(math.Sqrt(float64(thrN))))
	thrN /= 4
	if thrN < 1 {
		thrN = 1
	}
	uuidsConds := []string{}
	nUUIDs := len(uuids)
	nConds := nUUIDs / termsSize
	if nUUIDs%termsSize != 0 {
		nConds++
	}
	uuidsAry := []string{}
	for uuid := range uuids {
		uuidsAry = append(uuidsAry, uuid)
	}
	for i := 0; i < nConds; i++ {
		uuidsCond := "author_uuid in ("
		from := i * termsSize
		to := from + termsSize
		if to > nUUIDs {
			to = nUUIDs
		}
		for j := from; j < to; j++ {
			uuidsCond += "'" + uuidsAry[j] + "',"
		}
		uuidsCond = uuidsCond[0:len(uuidsCond)-1] + ")"
		uuidsConds = append(uuidsConds, uuidsCond)
	}
	fmt.Printf("UUIDs processing in %d packs\n", len(uuidsConds))
	var mMtx *sync.Mutex
	if thrN > 1 {
		mMtx = &sync.Mutex{}
	}
	processSlug := func(ch chan error, es, slug, pattern, cond string) (err error) {
		if ch != nil {
			defer func() {
				if err != nil {
					err = errs.Wrap(err, "processSlug: "+slug)
				}
				ch <- err
			}()
		}
		// fmt.Printf("Processing: %s <--> %s\n", slug, pattern)
		data := fmt.Sprintf(
			`{"query":"select author_uuid from \"%s\" where author_uuid is not null and author_uuid != '' and `+cond+` group by author_uuid","fetch_size":%d}`,
			jsonEscape(pattern),
			fetchSize,
		)
		payloadBytes := []byte(data)
		payloadBody := bytes.NewReader(payloadBytes)
		method := "POST"
		url := fmt.Sprintf("%s/_sql?format=json", es)
		var req *http.Request
		req, err = http.NewRequest(method, url, payloadBody)
		if err != nil {
			err = fmt.Errorf("new request error: %+v for %s url: %s, data: %s\n", err, method, url, data)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		var resp *http.Response
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			err = fmt.Errorf("do request error: %+v for %s url: %s, data: %s\n", err, method, url, data)
			return
		}
		var body []byte
		body, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			err = fmt.Errorf("ReadAll non-ok request error: %+v for %s url: %s, data: %s\n", err, method, url, data)
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			err = fmt.Errorf("Method:%s url:%s data: %s status:%d\n%s\n", method, url, data, resp.StatusCode, body)
			return
		}
		type uuidsResult struct {
			Cursor string     `json:"cursor"`
			Rows   [][]string `json:"rows"`
		}
		var result uuidsResult
		err = json.Unmarshal(body, &result)
		if err != nil {
			err = fmt.Errorf("Unmarshal error: %+v", err)
			return
		}
		slugUUIDs := []string{}
		for _, row := range result.Rows {
			slugUUIDs = append(slugUUIDs, row[0])
		}
		if len(result.Rows) == 0 {
			return
		}
		for {
			data = `{"cursor":"` + result.Cursor + `"}`
			payloadBytes = []byte(data)
			payloadBody = bytes.NewReader(payloadBytes)
			req, err = http.NewRequest(method, url, payloadBody)
			if err != nil {
				err = fmt.Errorf("new request error: %+v for %s url: %s, data: %s\n", err, method, url, data)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err = http.DefaultClient.Do(req)
			if err != nil {
				err = fmt.Errorf("do request error: %+v for %s url: %s, data: %s\n", err, method, url, data)
				return
			}
			body, err = ioutil.ReadAll(resp.Body)
			if err != nil {
				err = fmt.Errorf("ReadAll non-ok request error: %+v for %s url: %s, data: %s\n", err, method, url, data)
				return
			}
			_ = resp.Body.Close()
			if resp.StatusCode != 200 {
				err = fmt.Errorf("Method:%s url:%s data: %s status:%d\n%s\n", method, url, data, resp.StatusCode, body)
				return
			}
			err = json.Unmarshal(body, &result)
			if err != nil {
				err = fmt.Errorf("Unmarshal error: %+v", err)
				return
			}
			if len(result.Rows) == 0 {
				break
			}
			for _, row := range result.Rows {
				slugUUIDs = append(slugUUIDs, row[0])
			}
		}
		url = fmt.Sprintf("%s/_sql/close", es)
		data = `{"cursor":"` + result.Cursor + `"}`
		payloadBytes = []byte(data)
		payloadBody = bytes.NewReader(payloadBytes)
		req, err = http.NewRequest(method, url, payloadBody)
		if err != nil {
			err = fmt.Errorf("new request error: %+v for %s url: %s, data: %s\n", err, method, url, data)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			err = fmt.Errorf("do request error: %+v for %s url: %s, data: %s\n", err, method, url, data)
			return
		}
		body, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			err = fmt.Errorf("ReadAll non-ok request error: %+v for %s url: %s, data: %s\n", err, method, url, data)
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			err = fmt.Errorf("Method:%s url:%s data: %s status:%d\n%s\n", method, url, data, resp.StatusCode, body)
			return
		}
		// fmt.Printf("%s --> %d uuids\n", slug, len(slugUUIDs))
		if mMtx != nil {
			mMtx.Lock()
			defer mMtx.Unlock()
		}
		for _, uuid := range slugUUIDs {
			_, ok := m[uuid]
			if !ok {
				m[uuid] = make(map[string]struct{})
			}
			m[uuid][slug] = struct{}{}
		}
		return
	}
	fmt.Printf("Using %d threads to process %d CNCF projects and %d UUIDs\n", thrN, len(slugs), len(uuids))
	if thrN > 1 {
		ch := make(chan error)
		nThreads := 0
		for _, slug := range slugs {
			for _, uuidsCond := range uuidsConds {
				pattern := slug2pattern[slug]
				go func(ch chan error, es, slug, pattern, uuidsCond string) {
					_ = processSlug(ch, es, slug, pattern, uuidsCond)
				}(ch, es, slug, pattern, uuidsCond)
				nThreads++
				if nThreads == thrN {
					err := <-ch
					if err != nil {
						fmt.Printf("%+v\n", err)
					}
					nThreads--
				}
			}
		}
		for nThreads > 0 {
			<-ch
			nThreads--
		}
	} else {
		for _, slug := range slugs {
			for _, uuidsCond := range uuidsConds {
				err := processSlug(nil, es, slug, slug2pattern[slug], uuidsCond)
				if err != nil {
					fmt.Printf("%+v\n", err)
				}
			}
		}
	}
	return
}

// mapCompanyName: maps company name to possibly new company name (when one was acquired by the another)
// If mapping happens, store it in the cache for speed
// stat:
// --- [no_regexp_match, cache] (unmapped)
// Company_name [match_regexp, match_cache]
func mapCompanyName(comMap map[string][2]string, acqMap map[*regexp.Regexp]string, stat map[string][2]int, company string) string {
	res, ok := comMap[company]
	if ok {
		if res[1] == "m" {
			ary := stat[res[0]]
			ary[1]++
			stat[res[0]] = ary
		} else {
			ary := stat["---"]
			ary[1]++
			stat["---"] = ary
		}
		return res[0]
	}
	for re, res := range acqMap {
		if re.MatchString(company) {
			comMap[company] = [2]string{res, "m"}
			ary := stat[res]
			ary[0]++
			stat[res] = ary
			return res
		}
	}
	comMap[company] = [2]string{company, "u"}
	ary := stat["---"]
	ary[0]++
	stat["---"] = ary
	return company
}

func updateProfile(db *sql.DB, uuid string, user *gitHubUser, countryCodes map[string]struct{}) bool {
	var cols []string
	var args []interface{}
	if user.Sex != nil && (*user.Sex == "m" || *user.Sex == "f") {
		gender := "male"
		if *user.Sex == "f" {
			gender = "female"
		}
		cols = append(cols, "gender = ?")
		args = append(args, gender)
	}
	if user.SexProb != nil {
		cols = append(cols, "gender_acc = ?")
		args = append(args, int(*user.SexProb*100.0))
	}
	if user.CountryID != nil {
		_, ok := countryCodes[strings.ToLower(*user.CountryID)]
		if !ok {
			fmt.Printf("Sorting Hat database has no '%s' country code, skipping country code update\n", *user.CountryID)
		} else {
			cols = append(cols, "country_code = ?")
			args = append(args, strings.ToUpper(*user.CountryID))
		}
	}
	if len(cols) > 0 {
		query := strings.Join(cols, ", ")
		query = "update profiles set " + query + " where uuid = ?"
		args = append(args, uuid)
		res, err := db.Exec(query, args...)
		if err != nil {
			fmt.Printf("%s %+v\n", query, args)
		}
		fatalOnError(err)
		count, err := res.RowsAffected()
		fatalOnError(err)
		return count > 0
	}
	return false
}

func updateBots(db *sql.DB) {
	query := "update profiles set is_bot = 1 " +
		"where uuid in (select distinct uuid from identities where (" +
		"username like 'cf-buildpacks-eng' or username like 'bosh-ci-push-pull' or username like 'gprasath' or " +
		"username like 'zephyr-github' or username like 'zephyrbot' or username like 'strimzi-ci' or " +
		"username like 'athenabot' or username like 'k8s-reviewable' or username like 'codecov-io' or " +
		"username like 'grpc-testing' or username like 'k8s-teamcity-mesosphere' or username like 'angular-builds' or " +
		"username like 'devstats-sync' or username like 'googlebot' or username like 'hibernate-ci' or " +
		"username like 'coveralls' or username like 'rktbot' or username like 'coreosbot' or username like 'web-flow' or " +
		"username like 'prometheus-roobot' or username like 'cncf-bot' or username like 'kernelprbot' or " +
		"username like 'istio-testing' or username like 'spinnakerbot' or username like 'pikbot' or " +
		"username like 'spinnaker-release' or username like 'golangcibot' or username like 'opencontrail-ci-admin' or " +
		"username like 'titanium-octobot' or username like 'asfgit' or username like 'appveyorbot' or " +
		"username like 'cadvisorjenkinsbot' or username like 'gitcoinbot' or username like 'katacontainersbot' or " +
		"username like 'prombot' or username like 'prowbot' or username like 'travis%bot' or username like 'k8s-%' or " +
		"username like '%-bot' or username like '%-robot' or username like 'bot-%' or username like 'robot-%' or " +
		"username like '%[bot]%' or username like '%[robot]%' or username like '%-jenkins' or username like 'jenkins-%' or " +
		"username like '%-ci%bot' or username like '%-testing' or username like 'codecov-%' or username like '%clabot%' or " +
		"username like '%cla-bot%' or username like '%-gerrit' or username like '%-bot-%' or " +
		"username like '%envoy-filter-example%' or username like '%cibot' or username like '%-ci') " +
		"and source like 'git%')"
	res, err := db.Exec(query)
	if err != nil {
		fmt.Printf("%s\n", query)
	}
	fatalOnError(err)
	count, err := res.RowsAffected()
	fatalOnError(err)
	fmt.Printf("Set %d profiles as bots (using identity username)\n", count)
	query = "update profiles set is_bot = 1 where name in (" +
		"'envoy-filter-example(CircleCI)', 'envoy-docs(travis)', 'data-plane-api(CircleCI)', " +
		"'go-control-plane(CircleCI)', 'Kubernetes Publisher')"
	res, err = db.Exec(query)
	if err != nil {
		fmt.Printf("%s\n", query)
	}
	fatalOnError(err)
	count, err = res.RowsAffected()
	fatalOnError(err)
	fmt.Printf("Set %d profiles as bots (using profile name)\n", count)
	// select p.uuid, p.name, p.email, p.is_bot, i.name, i.email, i.username, i.source
	// from identities i, profiles p where i.uuid = p.uuid and i.uuid in (select uuid from profiles where name in (...));
}

func addOrganization(db *sql.DB, company string) int {
	_, err := db.Exec("insert into organizations(name) values(?)", company)
	if err != nil {
		if strings.Contains(err.Error(), "Error 1062") {
			rows, err2 := db.Query("select name from organizations where name = ?", company)
			fatalOnError(err2)
			var existingName string
			for rows.Next() {
				fatalOnError(rows.Scan(&existingName))
			}
			fatalOnError(rows.Err())
			fatalOnError(rows.Close())
			fmt.Printf("Warning: name collision: trying to insert '%s', exists: '%s'\n", company, existingName)
		} else {
			fatalOnError(err)
		}
	}
	rows, err := db.Query("select id from organizations where name = ?", company)
	fatalOnError(err)
	var id int
	for rows.Next() {
		fatalOnError(rows.Scan(&id))
	}
	fatalOnError(rows.Err())
	fatalOnError(rows.Close())
	return id
}

func addEnrollment(db *sql.DB, uuid string, companyID int, from, to time.Time, m map[string]map[string]struct{}, replace bool) bool {
	slugs, ok := m[uuid]
	if !ok {
		return false
	}
	for slug := range slugs {
		var (
			dummy int
			err   error
		)
		if !replace {
			var rows *sql.Rows
			rows, err = db.Query("select 1 from enrollments where uuid = ? and start = ? and end = ? and organization_id = ? and project_slug = ?", uuid, from, to, companyID, slug)
			fatalOnError(err)
			for rows.Next() {
				fatalOnError(rows.Scan(&dummy))
			}
			fatalOnError(rows.Err())
			fatalOnError(rows.Close())
		}
		if dummy == 1 {
			return false
		}
		_, err = db.Exec("delete from enrollments where uuid = ? and start = ? and end = ? and project_slug = ?", uuid, from, to, slug)
		fatalOnError(err)
		_, err = db.Exec("insert into enrollments(uuid, start, end, organization_id, project_slug) values(?, ?, ?, ?, ?)", uuid, from, to, companyID, slug)
		fatalOnError(err)
	}
	return true
}

func updateIdentities(db *sql.DB, uuids map[string]struct{}) int64 {
	if len(uuids) == 0 {
		fmt.Printf("No identities to update.\n")
		return 0
	}
	var allUpdated int64
	n := 0
	pack := 0
	packSize := 1000
	queryRoot := "update identities set last_modified = now() where uuid in("
	query := queryRoot
	args := []interface{}{}
	for uuid := range uuids {
		query += "?,"
		args = append(args, uuid)
		n++
		if n == packSize {
			query = query[:len(query)-1] + ")"
			res, err := db.Exec(query, args...)
			if err != nil {
				fmt.Printf("%s %+v\n", query, args)
			}
			fatalOnError(err)
			updated, err := res.RowsAffected()
			fatalOnError(err)
			n = 0
			pack++
			query = queryRoot
			args = []interface{}{}
			allUpdated += updated
			fmt.Printf("Pack %d updated: %d/%d\n", pack, updated, packSize)
		}
	}
	if n > 0 {
		query = query[:len(query)-1] + ")"
		res, err := db.Exec(query, args...)
		if err != nil {
			fmt.Printf("%s %+v\n", query, args)
		}
		fatalOnError(err)
		updated, err := res.RowsAffected()
		fatalOnError(err)
		allUpdated += updated
		fmt.Printf("Last Pack updated: %d/%d\n", updated, n)
	}
	return allUpdated
}

func importAffs(db *sql.DB, users *gitHubUsers, acqs *allAcquisitions, esURL string, cncfSlugs []string) {
	// Process acquisitions
	fmt.Printf("Acquisitions: %+v\n", acqs.Acquisitions)
	var (
		acqMap map[*regexp.Regexp]string
		comMap map[string][2]string
		stat   map[string][2]int
	)
	onlyGithub := false
	if os.Getenv("ONLY_GITHUB") != "" {
		onlyGithub = true
	}
	replace := false
	if os.Getenv("REPLACE") != "" {
		replace = true
	}
	var re *regexp.Regexp
	acqMap = make(map[*regexp.Regexp]string)
	comMap = make(map[string][2]string)
	stat = make(map[string][2]int)
	srcMap := make(map[string]string)
	resMap := make(map[string]struct{})
	idxMap := make(map[*regexp.Regexp]int)
	for idx, acq := range acqs.Acquisitions {
		re = regexp.MustCompile(acq[0])
		res, ok := srcMap[acq[0]]
		if ok {
			fatalf("Acquisition number %d '%+v' is already present in the mapping and maps into '%s'", idx, acq, res)
		}
		srcMap[acq[0]] = acq[1]
		_, ok = resMap[acq[1]]
		if ok {
			fatalf("Acquisition number %d '%+v': some other acquisition already maps into '%s', merge them", idx, acq, acq[1])
		}
		resMap[acq[1]] = struct{}{}
		acqMap[re] = acq[1]
		idxMap[re] = idx
	}
	for re, res := range acqMap {
		i := idxMap[re]
		for idx, acq := range acqs.Acquisitions {
			if re.MatchString(acq[1]) && i != idx {
				fatalf("Acquisition's number %d '%s' result '%s' matches other acquisition number %d '%s' which maps to '%s', simplify it: '%v' -> '%s'", idx, acq[0], acq[1], i, re, res, acq[0], res)
			}
			if re.MatchString(acq[0]) && res != acq[1] {
				fatalf("Acquisition's number %d '%s' regexp '%s' matches other acquisition number %d '%s' which maps to '%s': result is different '%s'", idx, acq, acq[0], i, re, res, acq[1])
			}
		}
	}

	// Eventually clean affiliations data
	if os.Getenv("SH_CLEANUP") != "" {
		_, err := db.Exec("delete from enrollments where project_slug like 'cncf/%'")
		fatalOnError(err)
		_, err = db.Exec("delete from organizations")
		fatalOnError(err)
		fmt.Printf("Current affiliation data cleaned.\n")
	}

	// Fetch existing identities
	rows, err := db.Query("select uuid, email, username, source from identities")
	fatalOnError(err)
	var uuid string
	var email string
	var username string
	var pemail *string
	var pusername *string
	var source string
	email2uuid := make(map[string]string)
	username2uuid := make(map[string]string)
	for rows.Next() {
		fatalOnError(rows.Scan(&uuid, &pemail, &pusername, &source))
		email = ""
		username = ""
		if pemail != nil {
			email = *pemail
		}
		if pusername != nil {
			username = *pusername
		}
		email2uuid[email] = uuid
		if !onlyGithub || source == "git" || source == "github" {
			username2uuid[username] = uuid
		}
	}
	fatalOnError(rows.Err())
	fatalOnError(rows.Close())

	testConnect := os.Getenv("SH_TEST_CONNECT")
	if testConnect != "" {
		fmt.Printf("Test mode: connection ok\n")
		return
	}

	// Fetch current organizations
	rows, err = db.Query("select id, name from organizations")
	fatalOnError(err)
	var name string
	var id int
	oname2id := make(map[string]int)
	for rows.Next() {
		fatalOnError(rows.Scan(&id, &name))
		oname2id[strings.ToLower(name)] = id
	}
	fatalOnError(rows.Err())
	fatalOnError(rows.Close())

	// Fetch known country codes
	countryCodes := make(map[string]struct{})
	rows, err = db.Query("select code from countries")
	fatalOnError(err)
	var code string
	for rows.Next() {
		fatalOnError(rows.Scan(&code))
		countryCodes[strings.ToLower(code)] = struct{}{}
	}
	fatalOnError(rows.Err())
	fatalOnError(rows.Close())

	// Process all JSON entries
	noProfileUpdate := false
	noProfileUpdateS := os.Getenv("NO_PROFILE_UPDATE")
	if noProfileUpdateS != "" {
		noProfileUpdate = true
	}
	defaultStartDate := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	defaultEndDate := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	companies := make(stringSet)
	var affList []affData
	hits := 0
	allAffs := 0
	updatedProfiles := make(map[string]struct{})
	notUpdatedProfiles := make(map[string]struct{})
	allUUIDs := make(map[string]struct{})
	for _, user := range *users {
		// Email decode ! --> @
		user.Email = strings.ToLower(emailDecode(user.Email))
		email := user.Email
		login := user.Login
		// Update profiles
		uuids := make(map[string]struct{})
		uuid, ok := email2uuid[email]
		if ok {
			uuids[uuid] = struct{}{}
		}
		uuid, ok = username2uuid[login]
		if ok {
			uuids[uuid] = struct{}{}
		}
		if len(uuids) > 0 {
			for uuid := range uuids {
				allUUIDs[uuid] = struct{}{}
				updated := false
				if !noProfileUpdate {
					updated = updateProfile(db, uuid, &user, countryCodes)
				}
				if updated {
					updatedProfiles[uuid] = struct{}{}
				} else {
					notUpdatedProfiles[uuid] = struct{}{}
				}
			}
			hits++
			// Affiliations
			affs := user.Affiliation
			if affs == "NotFound" || affs == "(Unknown)" || affs == "?" || affs == "-" || affs == "" {
				continue
			}
			affsAry := strings.Split(affs, ", ")
			prevDate := defaultStartDate
			for _, aff := range affsAry {
				var dtFrom, dtTo time.Time
				ary := strings.Split(aff, " < ")
				company := strings.TrimSpace(ary[0])
				if len(ary) > 1 {
					// "company < date" form
					dtFrom = prevDate
					dtTo = timeParseAny(ary[1])
				} else {
					// "company" form
					dtFrom = prevDate
					dtTo = defaultEndDate
				}
				if company == "" {
					continue
				}
				// Map using companies acquisitions/company names mapping
				company = mapCompanyName(comMap, acqMap, stat, company)
				companies[company] = struct{}{}
				for uuid := range uuids {
					affList = append(affList, affData{uuid: uuid, company: company, from: dtFrom, to: dtTo})
					allAffs++
				}
				prevDate = dtTo
			}
		}
	}
	// fmt.Printf("affList: %+v\ncompanies: %+v\n", affList, companies)
	// fmt.Printf("oname2id: %+v\ncompanies: %+v\n", oname2id, companies)
	fmt.Printf("All UUIDs: %d\n", len(allUUIDs))
	uuids2slugs := getUUIDsProjects(esURL, cncfSlugs, allUUIDs)
	counts := make(map[int]int)
	for _, slugs := range uuids2slugs {
		count := len(slugs)
		cnt, ok := counts[count]
		if !ok {
			counts[count] = 1
		} else {
			counts[count] = cnt + 1
		}
	}
	sInfo := []string{}
	for count, n := range counts {
		sInfo = append(sInfo, fmt.Sprintf("%03d projects: %d uuids\n", count, n))
	}
	sort.Strings(sInfo)
	for _, info := range sInfo {
		fmt.Print(info)
	}
	miss := 0
	for uuid := range allUUIDs {
		_, ok := uuids2slugs[uuid]
		if !ok {
			miss++
		}
	}
	fmt.Printf("%d uuids not found\n", miss)

	// Add companies
	for company := range companies {
		if company == "" {
			continue
		}
		lCompany := strings.ToLower(company)
		id, ok := oname2id[lCompany]
		if !ok {
			id = addOrganization(db, company)
			oname2id[lCompany] = id
		}
	}
	fmt.Printf("Processed %d companies\n", len(companies))

	// Add enrollments
	updatedEnrollments := make(map[string]struct{})
	notUpdatedEnrollments := make(map[string]struct{})
	nAffs := len(affList)
	for i, aff := range affList {
		uuid := aff.uuid
		if aff.company == "" {
			continue
		}
		lCompany := strings.ToLower(aff.company)
		companyID, ok := oname2id[lCompany]
		if !ok {
			fatalf("company not found: " + aff.company)
		}
		updated := addEnrollment(db, uuid, companyID, aff.from, aff.to, uuids2slugs, replace)
		if updated {
			updatedEnrollments[uuid] = struct{}{}
		} else {
			notUpdatedEnrollments[uuid] = struct{}{}
		}
		if i > 0 && i%1000 == 0 {
			fmt.Printf("Processed %d/%d enrollments\n", i, nAffs)
		}
	}
	fmt.Printf("Processed %d affiliations\n", len(affList))

	// Gather uuids updated and update their 'last_modified' date on 'identities' table
	updatedUuids := make(map[string]struct{})
	for uuid := range updatedProfiles {
		updatedUuids[uuid] = struct{}{}
	}
	for uuid := range updatedEnrollments {
		updatedUuids[uuid] = struct{}{}
	}
	notUpdatedUuids := make(map[string]struct{})
	for uuid := range notUpdatedProfiles {
		notUpdatedUuids[uuid] = struct{}{}
	}
	for uuid := range notUpdatedEnrollments {
		notUpdatedUuids[uuid] = struct{}{}
	}
	updates := updateIdentities(db, updatedUuids)
	fmt.Printf(
		"Hits: %d, affiliations: %d, companies: %d, updated profiles: %d, updated enrollments: %d, updated uuids: %d, "+
			"actual updates: %d, not updated profiles: %d, not updated enrollments: %d, not updated uuids: %d\n",
		hits,
		allAffs,
		len(companies),
		len(updatedProfiles),
		len(updatedEnrollments),
		len(updatedUuids),
		updates,
		len(notUpdatedProfiles),
		len(notUpdatedEnrollments),
		len(notUpdatedUuids),
	)
	for company, data := range stat {
		if company == "---" {
			fmt.Printf("Non-acquired companies: checked all regexp: %d, cache hit: %d\n", data[0], data[1])
		} else {
			fmt.Printf("Mapped to '%s': checked regexp: %d, cache hit: %d\n", company, data[0], data[1])
		}
	}
	for company, data := range comMap {
		if data[1] == "u" {
			continue
		}
		fmt.Printf("Used mapping '%s' --> '%s'\n", company, data[0])
	}
	updateBots(db)
	fmt.Printf("All finished OK, you should run map_org_names DA affiliation API now\n")
}

// getConnectString - get MariaDB SH (Sorting Hat) database DSN
// Either provide full DSN via SH_DSN='shuser:shpassword@tcp(shhost:shport)/shdb?charset=utf8'
// Or use some SH_ variables, only SH_PASS is required
// Defaults are: "shuser:required_pwd@tcp(localhost:3306)/shdb?charset=utf8
// SH_DSN has higher priority; if set no SH_ varaibles are used
func getConnectString() string {
	//dsn := "shuser:"+os.Getenv("PASS")+"@/shdb?charset=utf8")
	dsn := os.Getenv("SH_DSN")
	if dsn == "" {
		pass := os.Getenv("SH_PASS")
		if pass == "" {
			fatalf("please specify database password via SH_PASS=...")
		}
		user := os.Getenv("SH_USER")
		if user == "" {
			user = "shuser"
		}
		proto := os.Getenv("SH_PROTO")
		if proto == "" {
			proto = "tcp"
		}
		host := os.Getenv("SH_HOST")
		if host == "" {
			host = "localhost"
		}
		port := os.Getenv("SH_PORT")
		if port == "" {
			port = "3306"
		}
		db := os.Getenv("SH_DB")
		if db == "" {
			db = "shdb"
		}
		params := os.Getenv("SH_PARAMS")
		if params == "" {
			params = "?charset=utf8"
		}
		if params == "-" {
			params = ""
		}
		dsn = fmt.Sprintf(
			"%s:%s@%s(%s:%s)/%s%s",
			user,
			pass,
			proto,
			host,
			port,
			db,
			params,
		)
	}
	return dsn
}

// getAffiliationsJSONBody - get affiliations JSON contents
// First try to get JSON from SH_LOCAL_JSON_PATH which defaults to "github_users.json"
// Fallback to SH_REMOTE_JSON_PATH which defaults to "https://github.com/cncf/devstats/raw/master/github_users.json"
func getAffiliationsJSONBody() []byte {
	jsonLocalPath := os.Getenv("SH_LOCAL_JSON_PATH")
	if jsonLocalPath == "" {
		jsonLocalPath = "github_users.json"
	}
	data, err := ioutil.ReadFile(jsonLocalPath)
	if err != nil {
		switch err := err.(type) {
		case *os.PathError:
			jsonRemotePath := os.Getenv("SH_REMOTE_JSON_PATH")
			if jsonRemotePath == "" {
				jsonRemotePath = "https://github.com/cncf/devstats/raw/master/github_users.json"
			}
			response, err2 := http.Get(jsonRemotePath)
			fatalOnError(err2)
			defer func() { _ = response.Body.Close() }()
			data, err2 = ioutil.ReadAll(response.Body)
			fatalOnError(err2)
			fmt.Printf("Read %d bytes remote JSON data from %s\n", len(data), jsonRemotePath)
			return data
		default:
			fatalOnError(err)
		}
	}
	fmt.Printf("Read %d bytes local JSON data from %s\n", len(data), jsonLocalPath)
	return data
}

// getAcquisitionsYAMLBody - get company acquisitions and name mappings YAML body
// First try to get YAML from SH_LOCAL_YAML_PATH which defaults to "companies.yaml"
// Fallback to SH_REMOTE_YAML_PATH which defaults to "https://github.com/cncf/devstats/raw/master/companies.yaml"
func getAcquisitionsYAMLBody() []byte {
	yamlLocalPath := os.Getenv("SH_LOCAL_YAML_PATH")
	if yamlLocalPath == "" {
		yamlLocalPath = "companies.yaml"
	}
	data, err := ioutil.ReadFile(yamlLocalPath)
	if err != nil {
		switch err := err.(type) {
		case *os.PathError:
			yamlRemotePath := os.Getenv("SH_REMOTE_YAML_PATH")
			if yamlRemotePath == "" {
				yamlRemotePath = "https://github.com/cncf/devstats/raw/master/companies.yaml"
			}
			response, err2 := http.Get(yamlRemotePath)
			fatalOnError(err2)
			defer func() { _ = response.Body.Close() }()
			data, err2 = ioutil.ReadAll(response.Body)
			fatalOnError(err2)
			fmt.Printf("Read %d bytes remote YAML data from %s\n", len(data), yamlRemotePath)
			return data
		default:
			fatalOnError(err)
		}
	}
	fmt.Printf("Read %d bytes local YAML data from %s\n", len(data), yamlLocalPath)
	return data
}

func getCNCFSlugs(repoURL string) (slugs []string, err error) {
	cmd := []string{"git", "clone", "--single-branch", "--branch", "prod", repoURL}
	env := map[string]string{"GIT_TERMINAL_PROMPT": "0"}
	_, _ = execCommand(cmd, env)
	defer func() {
		_, _ = execCommand([]string{"rm", "-rf", "dev-analytics-api"}, nil)
	}()
	var fns string
	fns, _ = execCommand([]string{"ls", "./dev-analytics-api/app/services/lf/bootstrap/fixtures/cncf"}, nil)
	fna := strings.Split(fns, "\n")
	for _, fn := range fna {
		fn = strings.TrimSpace(fn)
		if fn == "" {
			continue
		}
		fn = "./dev-analytics-api/app/services/lf/bootstrap/fixtures/cncf/" + fn
		var data []byte
		data, err = ioutil.ReadFile(fn)
		fatalOnError(err)
		var fixture fixtureData
		err = yaml.Unmarshal(data, &fixture)
		if fixture.Native.Slug == "" {
			fatalf("fixture %+v has no slug", fixture)
		}
		slugs = append(slugs, fixture.Native.Slug)
	}
	return
}

func main() {
	// Connect to MariaDB
	dsn := getConnectString()
	db, err := sql.Open("mysql", dsn)
	fatalOnError(err)
	defer func() { fatalOnError(db.Close()) }()
	_, err = db.Exec("set @origin = ?", cOrigin)
	fatalOnError(err)

	esURL := os.Getenv("ES_URL")
	if esURL == "" {
		fatalf("you need to specify ES_URL env variable")
	}
	repoAccess := os.Getenv("REPO_ACCESS")
	if repoAccess == "" {
		fatalf("you need to specify REPO_ACCESS env variable")
	}

	// Get all CNCF projects slugs from DA-api repo
	var cncfSlugs []string
	cncfSlugs, err = getCNCFSlugs(repoAccess)
	fatalOnError(err)
	fmt.Printf("Found %d CNCF projects\n", len(cncfSlugs))

	// Parse github_users.json
	var users gitHubUsers
	// Read json data from local file falling back to remote file
	data := getAffiliationsJSONBody()
	fatalOnError(json.Unmarshal(data, &users))

	// Parse companies.yaml
	var acqs allAcquisitions
	// Read yaml data from local file falling back to remote file
	data = getAcquisitionsYAMLBody()
	fatalOnError(yaml.Unmarshal(data, &acqs))

	// Trigger sync event
	defer func() {
		e := ssawsync.Sync(cOrigin)
		if e != nil {
			fmt.Printf("ssaw sync error: %v\n", e)
		}
	}()
	// Import affiliations
	importAffs(db, &users, &acqs, esURL, cncfSlugs)
}
