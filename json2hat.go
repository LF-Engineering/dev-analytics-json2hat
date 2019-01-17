package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// gitHubUsers - list of GitHub user data from cncf/gitdm.
type gitHubUsers []gitHubUser

// gitHubUser - single GitHug user entry from cncf/gitdm `github_users.json` JSON.
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

func updateProfile(db *sql.DB, uuid string, user *gitHubUser, countryCodes map[string]struct{}) {
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
		_, err := db.Exec(query, args...)
		if err != nil {
			fmt.Printf("%s %+v\n", query, args)
		}
		fatalOnError(err)
	}
}

func addOrganization(db *sql.DB, company string) int {
	_, err := db.Exec("insert into organizations(name) values(?)", company)
	fatalOnError(err)
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

func addEnrollment(db *sql.DB, uuid string, companyID int, from, to time.Time) {
	_, err := db.Exec("delete from enrollments where uuid = ? and start = ? and end = ?", uuid, from, to)
	fatalOnError(err)
	_, err = db.Exec("insert into enrollments(uuid, start, end, organization_id) values(?, ?, ?, ?)", uuid, from, to, companyID)
	fatalOnError(err)
}

func importAffs(db *sql.DB, users *gitHubUsers) {
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
		if source == "git" || source == "github" {
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
	defaultStartDate := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	defaultEndDate := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	companies := make(stringSet)
	var affList []affData
	hits := 0
	allAffs := 0
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
				updateProfile(db, uuid, &user, countryCodes)
			}
			hits++
			// Affiliations
			affs := user.Affiliation
			if affs == "NotFound" || affs == "(Unknown)" || affs == "?" || affs == "" {
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

	// Add enrollments
	for _, aff := range affList {
		uuid := aff.uuid
		if aff.company == "" {
			continue
		}
		lCompany := strings.ToLower(aff.company)
		companyID, ok := oname2id[lCompany]
		if !ok {
			fatalf("company not found: " + aff.company)
		}
		addEnrollment(db, uuid, companyID, aff.from, aff.to)
	}
	fmt.Printf("Hits: %d, affiliations: %d, companies: %d\n", hits, allAffs, len(companies))
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
// Fallback to SH_REMOTE_JSON_PATH which defaults to "https://raw.githubusercontent.com/cncf/gitdm/master/github_users.json"
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
				jsonRemotePath = "https://raw.githubusercontent.com/cncf/gitdm/master/github_users.json"
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

func main() {
	// Connect to MariaDB
	dsn := getConnectString()
	db, err := sql.Open("mysql", dsn)
	fatalOnError(err)
	defer func() { fatalOnError(db.Close()) }()

	// Parse github_users.json
	var users gitHubUsers
	// Read json data from, local file falling back to remote file
	data := getAffiliationsJSONBody()
	fatalOnError(json.Unmarshal(data, &users))

	// Import affiliations
	importAffs(db, &users)
}
