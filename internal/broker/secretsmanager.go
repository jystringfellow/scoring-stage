package broker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	SecretsManagerPort = 9912
	TokenHeader        = "X-Stage-Broker-Token"
	jsonContentType    = "application/x-amz-json-1.1"
	maxRequestBody     = 64 << 10
	maxResponseBody    = 16 << 20
	maxBatchSecretIDs  = 20
	maxFilters         = 10
)

var (
	SecretsManagerAddress = "127.0.0.1:" + strconv.Itoa(SecretsManagerPort)
	regionName            = regexp.MustCompile(`^[a-z0-9-]+$`)
	secretARN             = regexp.MustCompile(`^arn:[a-z-]+:secretsmanager:([a-z0-9-]+):[0-9]{12}:secret:(.+)-[A-Za-z0-9]{6}$`)
	filterValue           = regexp.MustCompile(`^!?[a-zA-Z0-9 :_@/+=.\-!]{0,512}$`)
	listSortKeys          = []string{"created-date", "last-accessed-date", "last-changed-date", "name"}
	appliedFilterKeys     = []string{"name", "description", "tag-key", "tag-value"}
)

type SecretsManagerGrant struct {
	Region   string
	Profiles map[string]string
}

func (g Grants) IssueSecretsManager(owner, region string, byProfile map[string][]string) error {
	path := filepath.Join(g.Path, owner+".secrets")
	if len(byProfile) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if !regionName.MatchString(region) {
		return fmt.Errorf("region %q is not an AWS region name", region)
	}
	lines := []string{"region " + region}
	profiles := make([]string, 0, len(byProfile))
	for profile := range byProfile {
		profiles = append(profiles, profile)
	}
	sort.Strings(profiles)
	for _, profile := range profiles {
		for _, name := range slices.Sorted(slices.Values(byProfile[profile])) {
			lines = append(lines, "secret "+profile+" "+name)
		}
	}
	return writePrivate(path, strings.Join(lines, "\n")+"\n")
}

func (g Grants) LookupSecretsManager(supplied string) (string, SecretsManagerGrant, bool) {
	owner, found := g.owner(supplied)
	if !found {
		return "", SecretsManagerGrant{}, false
	}
	grant := SecretsManagerGrant{Profiles: map[string]string{}}
	content, err := os.ReadFile(filepath.Join(g.Path, owner+".secrets"))
	if err != nil {
		return owner, grant, true
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 2 && fields[0] == "region":
			grant.Region = fields[1]
		case len(fields) == 3 && fields[0] == "secret":
			grant.Profiles[fields[2]] = fields[1]
		}
	}
	return owner, grant, true
}

type cachedKey struct {
	key   accessKey
	until time.Time
}

type SecretsManager struct {
	Credentials Credentials
	Client      *http.Client
	Endpoint    func(region string) string
	mu          sync.Mutex
	keys        map[string]cachedKey
}

func NewSecretsManager(credentials Credentials) *SecretsManager {
	return &SecretsManager{
		Credentials: credentials,
		Client:      &http.Client{Timeout: 30 * time.Second},
		Endpoint:    func(region string) string { return "https://secretsmanager." + region + ".amazonaws.com/" },
		keys:        map[string]cachedKey{},
	}
}

func (s *SecretsManager) key(profile string) (accessKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, found := s.keys[profile]; found && now().Before(cached.until) {
		return cached.key, nil
	}
	raw, err := s.Credentials.CredentialProcess(profile, map[string]bool{profile: true})
	if err != nil {
		return accessKey{}, err
	}
	var exported struct{ AccessKeyId, SecretAccessKey, SessionToken, Expiration string }
	if err := json.Unmarshal(raw, &exported); err != nil {
		return accessKey{}, &Error{http.StatusBadGateway, profile + ": aws cli returned invalid JSON"}
	}
	expires, err := time.Parse(time.RFC3339, exported.Expiration)
	if err != nil {
		return accessKey{}, &Error{http.StatusBadGateway, profile + ": aws cli returned an unreadable Expiration"}
	}
	key := accessKey{exported.AccessKeyId, exported.SecretAccessKey, exported.SessionToken}
	s.keys[profile] = cachedKey{key, expires.Add(-5 * time.Minute)}
	return key, nil
}

type awsAnswer struct {
	status    int
	body      []byte
	requestID string
}

func (s *SecretsManager) call(profile, region, operation string, payload any) (awsAnswer, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return awsAnswer{}, err
	}
	key, err := s.key(profile)
	if err != nil {
		return awsAnswer{}, err
	}
	request, err := http.NewRequest(http.MethodPost, s.Endpoint(region), bytes.NewReader(body))
	if err != nil {
		return awsAnswer{}, err
	}
	request.Header.Set("Content-Type", jsonContentType)
	request.Header.Set("X-Amz-Target", "secretsmanager."+operation)
	signRequest(request, body, key, region, "secretsmanager", now())
	response, err := s.Client.Do(request)
	if err != nil {
		return awsAnswer{}, &Error{http.StatusServiceUnavailable, fmt.Sprintf("%s: cannot reach Secrets Manager in %s: %v", profile, region, err)}
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
	if err != nil || len(answer) > maxResponseBody {
		return awsAnswer{}, &Error{http.StatusBadGateway, fmt.Sprintf("%s: Secrets Manager's answer was unreadable or too large", profile)}
	}
	return awsAnswer{response.StatusCode, answer, response.Header.Get("X-Amzn-Requestid")}, nil
}

type refusal struct {
	status int
	kind   string
	msg    string
}

func (r *refusal) Error() string { return r.msg }

func refuse(format string, args ...any) *refusal {
	return &refusal{http.StatusBadRequest, "AccessDeniedException", "stage broker: " + fmt.Sprintf(format, args...)}
}

func asRefusal(err error) *refusal {
	var refused *refusal
	if errors.As(err, &refused) {
		return refused
	}
	var brokerErr *Error
	if errors.As(err, &brokerErr) {
		kind := "ServiceUnavailableException"
		if brokerErr.Status == http.StatusForbidden {
			kind = "AccessDeniedException"
		}
		return &refusal{brokerErr.Status, kind, "stage broker: " + brokerErr.Error()}
	}
	return &refusal{http.StatusInternalServerError, "InternalServiceError", "stage broker: " + err.Error()}
}

type secretsRequest struct {
	api   *SecretsManager
	grant SecretsManagerGrant
	read  []string
}

func (q *secretsRequest) resolve(id string) (name, profile string, listed bool) {
	if profile, listed := q.grant.Profiles[id]; listed {
		return id, profile, true
	}
	if match := secretARN.FindStringSubmatch(id); match != nil && match[1] == q.grant.Region {
		if profile, listed := q.grant.Profiles[match[2]]; listed {
			return match[2], profile, true
		}
	}
	return "", "", false
}

func (q *secretsRequest) noteRead(name string) {
	if !slices.Contains(q.read, name) {
		q.read = append(q.read, name)
	}
}

func decodeStrictly(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return refuse("cannot read the request: %v", err)
	}
	if decoder.More() {
		return refuse("cannot read the request: more than one JSON value")
	}
	return nil
}

func SecretsManagerHandler(api *SecretsManager, grants Grants, log func(string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner, operation := "-", "-"
		query := &secretsRequest{api: api}
		send := func(status int, body []byte, requestID string) {
			w.Header().Set("Content-Type", jsonContentType)
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			if requestID != "" {
				w.Header().Set("X-Amzn-Requestid", requestID)
			}
			w.WriteHeader(status)
			w.Write(body)
			read := "-"
			if len(query.read) > 0 {
				read = strings.Join(query.read, ",")
			}
			log(fmt.Sprintf("broker: %s secretsmanager %s %s %d", owner, operation, read, status))
		}
		fail := func(err error) {
			refused := asRefusal(err)
			body, _ := json.Marshal(map[string]string{"__type": refused.kind, "Message": refused.msg})
			query.read = nil
			send(refused.status, body, "")
		}
		if r.Method != http.MethodPost {
			fail(&refusal{http.StatusMethodNotAllowed, "AccessDeniedException", "stage broker: only POST is served"})
			return
		}
		tokens := r.Header.Values(TokenHeader)
		var grant SecretsManagerGrant
		found := false
		if len(tokens) == 1 {
			owner, grant, found = grants.LookupSecretsManager(tokens[0])
		}
		if !found {
			owner = "-"
			fail(&refusal{http.StatusUnauthorized, "UnrecognizedClientException", "stage broker: missing or unknown broker token"})
			return
		}
		query.grant = grant
		if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != jsonContentType {
			fail(refuse("Content-Type must be %s", jsonContentType))
			return
		}
		targets := r.Header.Values("X-Amz-Target")
		target := strings.Join(targets, ",")
		served := map[string]func([]byte) (awsAnswer, error){
			"secretsmanager.GetSecretValue":      query.getSecretValue,
			"secretsmanager.BatchGetSecretValue": query.batchGetSecretValue,
			"secretsmanager.ListSecrets":         query.listSecrets,
		}
		serve, known := served[target]
		if len(targets) != 1 || !known {
			fail(refuse("%s is not served; the stage broker serves GetSecretValue, BatchGetSecretValue and ListSecrets", escaped(target)))
			return
		}
		operation = strings.TrimPrefix(target, "secretsmanager.")
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody+1))
		if err != nil || len(body) > maxRequestBody {
			fail(refuse("the request body is larger than %d KiB", maxRequestBody>>10))
			return
		}
		answer, err := serve(body)
		if err != nil {
			fail(err)
			return
		}
		send(answer.status, answer.body, answer.requestID)
	})
}

func (q *secretsRequest) getSecretValue(body []byte) (awsAnswer, error) {
	var request struct {
		SecretId     string
		VersionId    *string `json:",omitempty"`
		VersionStage *string `json:",omitempty"`
	}
	if err := decodeStrictly(body, &request); err != nil {
		return awsAnswer{}, err
	}
	name, profile, listed := q.resolve(request.SecretId)
	if !listed {
		return awsAnswer{}, refuse("secret %s is not listed for this sandbox", escaped(request.SecretId))
	}
	answer, err := q.api.call(profile, q.grant.Region, "GetSecretValue", request)
	if err != nil || answer.status != http.StatusOK {
		return answer, err
	}
	var value struct{ Name string }
	if json.Unmarshal(answer.body, &value) != nil || value.Name != name {
		return awsAnswer{}, refuse("AWS answered for a different secret than %s, so the answer was dropped", name)
	}
	q.noteRead(name)
	return answer, nil
}

type secretError struct {
	SecretId  string
	ErrorCode string
	Message   string
}

func (q *secretsRequest) batchGetSecretValue(body []byte) (awsAnswer, error) {
	var request struct {
		SecretIdList []string
		Filters      json.RawMessage
		MaxResults   *int
		NextToken    *string
	}
	if err := decodeStrictly(body, &request); err != nil {
		return awsAnswer{}, err
	}
	switch {
	case request.Filters != nil:
		return awsAnswer{}, refuse("Filters are not served for BatchGetSecretValue; name the secrets in SecretIdList")
	case request.MaxResults != nil:
		return awsAnswer{}, refuse("MaxResults is not served for BatchGetSecretValue; it applies only with Filters")
	case request.NextToken != nil:
		return awsAnswer{}, refuse("NextToken is not served for BatchGetSecretValue; the stage broker answers in one page")
	case len(request.SecretIdList) == 0:
		return awsAnswer{}, refuse("SecretIdList is required")
	case len(request.SecretIdList) > maxBatchSecretIDs:
		return awsAnswer{}, refuse("SecretIdList takes at most %d secret ids", maxBatchSecretIDs)
	}
	values, failures := []json.RawMessage{}, []json.RawMessage{}
	addFailure := func(id, message string) {
		entry, _ := json.Marshal(secretError{id, "AccessDeniedException", "stage broker: " + message})
		failures = append(failures, entry)
	}
	idsByProfile, namesByID := map[string][]string{}, map[string]string{}
	for _, id := range request.SecretIdList {
		name, profile, listed := q.resolve(id)
		if !listed {
			addFailure(id, fmt.Sprintf("secret %s is not listed for this sandbox", escaped(id)))
			continue
		}
		idsByProfile[profile] = append(idsByProfile[profile], id)
		namesByID[id] = name
	}
	for _, profile := range slices.Sorted(maps.Keys(idsByProfile)) {
		ids := idsByProfile[profile]
		answer, err := q.api.call(profile, q.grant.Region, "BatchGetSecretValue", map[string]any{"SecretIdList": ids})
		if err != nil || answer.status != http.StatusOK {
			return answer, err
		}
		var page struct {
			SecretValues []json.RawMessage
			Errors       []json.RawMessage
		}
		if err := json.Unmarshal(answer.body, &page); err != nil {
			return awsAnswer{}, &Error{http.StatusBadGateway, profile + ": Secrets Manager's answer was unreadable"}
		}
		answered := map[string]bool{}
		for _, raw := range page.SecretValues {
			var value struct{ Name string }
			json.Unmarshal(raw, &value)
			for _, id := range ids {
				if namesByID[id] == value.Name && !answered[id] {
					answered[id] = true
					values = append(values, raw)
					q.noteRead(value.Name)
					break
				}
			}
		}
		failures = append(failures, page.Errors...)
		for _, id := range ids {
			if !answered[id] && !reportedIn(page.Errors, id) {
				addFailure(id, fmt.Sprintf("AWS answered for a different secret than %s, so the answer was dropped", namesByID[id]))
			}
		}
	}
	encoded, err := json.Marshal(map[string][]json.RawMessage{"SecretValues": values, "Errors": failures})
	return awsAnswer{status: http.StatusOK, body: encoded}, err
}

func reportedIn(errors []json.RawMessage, id string) bool {
	for _, raw := range errors {
		var entry secretError
		if json.Unmarshal(raw, &entry) == nil && entry.SecretId == id {
			return true
		}
	}
	return false
}

type listFilter struct {
	Key    string
	Values []string
}

type listedEntry struct {
	raw         json.RawMessage
	Name        string
	Description string
	Tags        []struct{ Key, Value string }
	dates       map[string]float64
}

func (q *secretsRequest) listSecrets(body []byte) (awsAnswer, error) {
	var request struct {
		Filters                []listFilter
		MaxResults             *int
		NextToken              *string
		SortOrder              *string
		SortBy                 *string
		IncludePlannedDeletion *bool
	}
	if err := decodeStrictly(body, &request); err != nil {
		return awsAnswer{}, err
	}
	if request.NextToken != nil {
		return awsAnswer{}, refuse("the stage broker returns every listed secret in one page, so it takes no NextToken")
	}
	sortBy, descending := "created-date", false
	if request.SortBy != nil {
		if !slices.Contains(listSortKeys, *request.SortBy) {
			return awsAnswer{}, refuse("SortBy %s is not one of %s", escaped(*request.SortBy), strings.Join(listSortKeys, ", "))
		}
		sortBy = *request.SortBy
	}
	if request.SortOrder != nil {
		if *request.SortOrder != "asc" && *request.SortOrder != "desc" {
			return awsAnswer{}, refuse("SortOrder %s is not asc or desc", escaped(*request.SortOrder))
		}
		descending = *request.SortOrder == "desc"
	}
	if len(request.Filters) > maxFilters {
		return awsAnswer{}, refuse("Filters takes at most %d entries", maxFilters)
	}
	for _, filter := range request.Filters {
		if !slices.Contains(appliedFilterKeys, filter.Key) {
			return awsAnswer{}, refuse("Filters key %s is not applied by the stage broker; it applies %s", escaped(filter.Key), strings.Join(appliedFilterKeys, ", "))
		}
		if len(filter.Values) == 0 || len(filter.Values) > maxFilters {
			return awsAnswer{}, refuse("a Filters entry needs Values, at most %d of them", maxFilters)
		}
		for _, value := range filter.Values {
			if !filterValue.MatchString(value) {
				return awsAnswer{}, refuse("Filters value %q is not a value Secrets Manager accepts", value)
			}
		}
	}
	namesByProfile := map[string][]string{}
	for name, profile := range q.grant.Profiles {
		namesByProfile[profile] = append(namesByProfile[profile], name)
	}
	var entries []listedEntry
	for _, profile := range slices.Sorted(maps.Keys(namesByProfile)) {
		names := slices.Sorted(slices.Values(namesByProfile[profile]))
		for start := 0; start < len(names); start += maxFilters {
			chunk := names[start:min(start+maxFilters, len(names))]
			found, answer, err := q.listPages(profile, chunk, request.IncludePlannedDeletion)
			if err != nil || answer.status != http.StatusOK {
				return answer, err
			}
			for _, entry := range found {
				if !slices.ContainsFunc(entries, func(seen listedEntry) bool { return seen.Name == entry.Name }) {
					entries = append(entries, entry)
				}
			}
		}
	}
	var kept []listedEntry
	for _, entry := range entries {
		if passesFilters(entry, request.Filters) {
			kept = append(kept, entry)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		first, second := kept[i], kept[j]
		if descending {
			first, second = second, first
		}
		if sortBy == "name" || first.dates[sortBy] == second.dates[sortBy] {
			return first.Name < second.Name
		}
		return first.dates[sortBy] < second.dates[sortBy]
	})
	list := make([]json.RawMessage, len(kept))
	for i, entry := range kept {
		list[i] = entry.raw
	}
	encoded, err := json.Marshal(map[string][]json.RawMessage{"SecretList": list})
	return awsAnswer{status: http.StatusOK, body: encoded}, err
}

func (q *secretsRequest) listPages(profile string, names []string, includePlannedDeletion *bool) ([]listedEntry, awsAnswer, error) {
	var found []listedEntry
	token := ""
	for {
		payload := map[string]any{"Filters": []listFilter{{Key: "name", Values: names}}, "MaxResults": 100}
		if token != "" {
			payload["NextToken"] = token
		}
		if includePlannedDeletion != nil {
			payload["IncludePlannedDeletion"] = *includePlannedDeletion
		}
		answer, err := q.api.call(profile, q.grant.Region, "ListSecrets", payload)
		if err != nil || answer.status != http.StatusOK {
			return nil, answer, err
		}
		var page struct {
			SecretList []json.RawMessage
			NextToken  string
		}
		if err := json.Unmarshal(answer.body, &page); err != nil {
			return nil, awsAnswer{}, &Error{http.StatusBadGateway, profile + ": Secrets Manager's answer was unreadable"}
		}
		for _, raw := range page.SecretList {
			var entry listedEntry
			var dates struct{ CreatedDate, LastAccessedDate, LastChangedDate float64 }
			if json.Unmarshal(raw, &entry) != nil || json.Unmarshal(raw, &dates) != nil || q.grant.Profiles[entry.Name] != profile || !slices.Contains(names, entry.Name) {
				continue
			}
			entry.raw = raw
			entry.dates = map[string]float64{"created-date": dates.CreatedDate, "last-accessed-date": dates.LastAccessedDate, "last-changed-date": dates.LastChangedDate}
			found = append(found, entry)
		}
		if page.NextToken == "" {
			return found, awsAnswer{status: http.StatusOK}, nil
		}
		token = page.NextToken
	}
}

func passesFilters(entry listedEntry, filters []listFilter) bool {
	for _, filter := range filters {
		positive, matchedPositive := false, false
		for _, value := range filter.Values {
			wanted, negated := strings.CutPrefix(value, "!")
			matched := fieldStartsWith(entry, filter.Key, wanted)
			if negated && matched {
				return false
			}
			if !negated {
				positive = true
				matchedPositive = matchedPositive || matched
			}
		}
		if positive && !matchedPositive {
			return false
		}
	}
	return true
}

func fieldStartsWith(entry listedEntry, key, prefix string) bool {
	switch key {
	case "name":
		return strings.HasPrefix(entry.Name, prefix)
	case "description":
		return strings.HasPrefix(strings.ToLower(entry.Description), strings.ToLower(prefix))
	case "tag-key":
		return slices.ContainsFunc(entry.Tags, func(tag struct{ Key, Value string }) bool { return strings.HasPrefix(tag.Key, prefix) })
	case "tag-value":
		return slices.ContainsFunc(entry.Tags, func(tag struct{ Key, Value string }) bool { return strings.HasPrefix(tag.Value, prefix) })
	}
	return false
}

func escaped(text string) string {
	return strings.Trim(strconv.QuoteToASCII(text), `"`)
}
