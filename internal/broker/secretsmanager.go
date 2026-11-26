package broker

import (
	"bytes"
	"context"
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
	maxPages           = 100
	maxSecretID        = 2048
	shownIDLength      = 64
	BrokerHeader       = "X-Stage-Broker"
	brokerHeaderValue  = "secrets-manager/1"
)

var (
	SecretsManagerAddress = "127.0.0.1:" + strconv.Itoa(SecretsManagerPort)
	regionName            = regexp.MustCompile(`^[a-z0-9-]+$`)
	secretARN             = regexp.MustCompile(`^arn:aws(?:-[a-z]{1,16}){0,3}:secretsmanager:([a-z0-9-]{1,32}):[0-9]{12}:secret:([A-Za-z0-9/_+=.@-]{1,512})-[A-Za-z0-9]{6}$`)
	filterValue           = regexp.MustCompile(`^!?[a-zA-Z0-9 :_@/+=.\-!]{0,512}$`)
	listSortKeys          = []string{"created-date", "last-accessed-date", "last-changed-date", "name"}
	awsErrorType          = regexp.MustCompile(`^[A-Za-z]{1,64}$`)
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

const failedLoginHold = 10 * time.Second

type cachedKey struct {
	key   accessKey
	err   error
	until time.Time
}

type SecretsManager struct {
	Credentials Credentials
	Client      *http.Client
	Endpoint    func(region string) string
	mu          sync.Mutex
	keys        map[string]cachedKey
	logins      map[string]chan struct{}
}

func NewSecretsManager(credentials Credentials) *SecretsManager {
	return &SecretsManager{
		Credentials: credentials,
		Client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("Secrets Manager answered with a redirect, which the broker does not follow")
			},
		},
		Endpoint: func(region string) string { return "https://secretsmanager." + region + ".amazonaws.com/" },
		keys:     map[string]cachedKey{},
		logins:   map[string]chan struct{}{},
	}
}

func (s *SecretsManager) login(profile string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logins[profile] == nil {
		s.logins[profile] = make(chan struct{}, 1)
	}
	return s.logins[profile]
}

func (s *SecretsManager) cached(profile string) (cachedKey, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cached, found := s.keys[profile]
	return cached, found && now().Before(cached.until)
}

func (s *SecretsManager) remember(profile string, entry cachedKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[profile] = entry
}

func (s *SecretsManager) key(ctx context.Context, profile string) (accessKey, error) {
	login := s.login(profile)
	select {
	case login <- struct{}{}:
	case <-ctx.Done():
		return accessKey{}, &Error{http.StatusServiceUnavailable, profile + ": the request ended while it waited for the host login"}
	}
	defer func() { <-login }()
	if cached, fresh := s.cached(profile); fresh {
		return cached.key, cached.err
	}
	key, err := s.export(profile)
	if err != nil {
		s.remember(profile, cachedKey{err: err, until: now().Add(failedLoginHold)})
		return accessKey{}, err
	}
	return key, nil
}

func (s *SecretsManager) export(profile string) (accessKey, error) {
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
	s.remember(profile, cachedKey{key: key, until: expires.Add(-5 * time.Minute)})
	return key, nil
}

type awsAnswer struct {
	status    int
	body      []byte
	requestID string
}

func (s *SecretsManager) call(ctx context.Context, profile, region, operation, about string, payload any) (awsAnswer, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return awsAnswer{}, err
	}
	key, err := s.key(ctx, profile)
	if err != nil {
		status, kind := http.StatusServiceUnavailable, "ServiceUnavailableException"
		var brokerErr *Error
		if errors.As(err, &brokerErr) && brokerErr.Status == http.StatusForbidden {
			status, kind = http.StatusForbidden, "AccessDeniedException"
		}
		return awsAnswer{}, &refusal{status, kind, "stage broker: cannot get AWS credentials for profile " + profile +
			" on the host; the broker's log on the host says why", err.Error()}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint(region), bytes.NewReader(body))
	if err != nil {
		return awsAnswer{}, err
	}
	request.Header.Set("Content-Type", jsonContentType)
	request.Header.Set("X-Amz-Target", "secretsmanager."+operation)
	signRequest(request, body, key, region, "secretsmanager", now())
	response, err := s.Client.Do(request)
	if err != nil {
		return awsAnswer{}, &refusal{http.StatusServiceUnavailable, "ServiceUnavailableException",
			"stage broker: cannot reach Secrets Manager; the broker's log on the host says why", fmt.Sprintf("%s in %s: %v", profile, region, err)}
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
	if err != nil || len(answer) > maxResponseBody {
		return awsAnswer{}, &refusal{http.StatusBadGateway, "InternalServiceError", "stage broker: Secrets Manager's answer was unreadable or too large", ""}
	}
	if response.StatusCode != http.StatusOK {
		kind, message := awsError(answer)
		return awsAnswer{}, &refusal{response.StatusCode, kind,
			fmt.Sprintf("stage broker: Secrets Manager answered %s for %s; the broker's log on the host has its message", kind, about), message}
	}
	return awsAnswer{response.StatusCode, answer, response.Header.Get("X-Amzn-Requestid")}, nil
}

func awsError(body []byte) (string, string) {
	var answer struct {
		Type     string `json:"__type"`
		Message  string `json:"Message"`
		Fallback string `json:"message"`
	}
	json.Unmarshal(body, &answer)
	kind := answer.Type
	if hash := strings.LastIndex(kind, "#"); hash >= 0 {
		kind = kind[hash+1:]
	}
	kind, _, _ = strings.Cut(kind, ":")
	if !awsErrorType.MatchString(kind) {
		kind = "InternalServiceError"
	}
	message := answer.Message
	if message == "" {
		message = answer.Fallback
	}
	return kind, message
}

type refusal struct {
	status int
	kind   string
	msg    string
	detail string
}

func (r *refusal) Error() string { return r.msg }

func refuse(format string, args ...any) *refusal {
	return &refusal{http.StatusBadRequest, "AccessDeniedException", "stage broker: " + fmt.Sprintf(format, args...), ""}
}

func asRefusal(err error) *refusal {
	var refused *refusal
	if errors.As(err, &refused) {
		return refused
	}
	return &refusal{http.StatusInternalServerError, "InternalServiceError", "stage broker: the request failed; the broker's log on the host says why", err.Error()}
}

type secretsRequest struct {
	api     *SecretsManager
	ctx     context.Context
	grant   SecretsManagerGrant
	read    []string
	details []string
}

type resolved struct{ id, name, profile, arn string }

func (q *secretsRequest) resolve(id string) (resolved, bool) {
	if profile, listed := q.grant.Profiles[id]; listed {
		return resolved{id, id, profile, ""}, true
	}
	if len(id) > maxSecretID {
		return resolved{}, false
	}
	if match := secretARN.FindStringSubmatch(id); match != nil && match[1] == q.grant.Region {
		if profile, listed := q.grant.Profiles[match[2]]; listed {
			return resolved{id, match[2], profile, id}, true
		}
	}
	return resolved{}, false
}

func (r resolved) answeredBy(name, arn string) bool {
	return name == r.name && (r.arn == "" || arn == r.arn)
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
		query := &secretsRequest{api: api, ctx: r.Context()}
		send := func(status int, body []byte, requestID string) {
			w.Header().Set(BrokerHeader, brokerHeaderValue)
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
			for _, detail := range query.details {
				log(fmt.Sprintf("broker: %s secretsmanager %s: %s", owner, operation, escaped(detail)))
			}
		}
		fail := func(err error) {
			refused := asRefusal(err)
			if refused.detail != "" {
				query.details = append(query.details, refused.detail)
			}
			body, _ := json.Marshal(map[string]string{"__type": refused.kind, "Message": refused.msg})
			query.read = nil
			send(refused.status, body, "")
		}
		if r.Method != http.MethodPost {
			fail(&refusal{http.StatusMethodNotAllowed, "AccessDeniedException", "stage broker: only POST / is served", ""})
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
			fail(&refusal{http.StatusUnauthorized, "UnrecognizedClientException", "stage broker: missing or unknown broker token", ""})
			return
		}
		query.grant = grant
		if r.URL.Path != "/" || r.URL.RawQuery != "" {
			fail(refuse("only POST / is served"))
			return
		}
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
	secret, listed := q.resolve(request.SecretId)
	if !listed {
		return awsAnswer{}, refuse("secret %s is not listed for this sandbox", shown(request.SecretId))
	}
	request.SecretId = secret.name
	answer, err := q.api.call(q.ctx, secret.profile, q.grant.Region, "GetSecretValue", secret.name, request)
	if err != nil {
		return answer, err
	}
	var value struct{ Name, ARN string }
	if json.Unmarshal(answer.body, &value) != nil || !secret.answeredBy(value.Name, value.ARN) {
		q.details = append(q.details, fmt.Sprintf("dropped an answer for %s (ARN %s) asked as %s", shown(value.Name), shown(value.ARN), shown(secret.id)))
		return awsAnswer{}, refuse("secret %s is not listed for this sandbox", shown(secret.id))
	}
	q.noteRead(secret.name)
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
	addFailure := func(id, code, message string) {
		entry, _ := json.Marshal(secretError{id, code, "stage broker: " + message})
		failures = append(failures, entry)
	}
	var wanted []resolved
	namesByProfile := map[string][]string{}
	for _, id := range request.SecretIdList {
		secret, listed := q.resolve(id)
		if !listed {
			addFailure(id, "AccessDeniedException", fmt.Sprintf("secret %s is not listed for this sandbox", shown(id)))
			continue
		}
		wanted = append(wanted, secret)
		if !slices.Contains(namesByProfile[secret.profile], secret.name) {
			namesByProfile[secret.profile] = append(namesByProfile[secret.profile], secret.name)
		}
	}
	type batchValue struct {
		raw       json.RawMessage
		Name, ARN string
	}
	answered, awsFailed := map[string]batchValue{}, map[string]string{}
	for _, profile := range slices.Sorted(maps.Keys(namesByProfile)) {
		names := namesByProfile[profile]
		token := ""
		for pages := 0; ; pages++ {
			if pages == maxPages {
				return awsAnswer{}, &refusal{http.StatusBadGateway, "InternalServiceError", fmt.Sprintf("stage broker: Secrets Manager answered in more than %d pages", maxPages), ""}
			}
			payload := map[string]any{"SecretIdList": names}
			if token != "" {
				payload["NextToken"] = token
			}
			answer, err := q.api.call(q.ctx, profile, q.grant.Region, "BatchGetSecretValue", strings.Join(names, ", "), payload)
			if err != nil {
				return answer, err
			}
			var page struct {
				SecretValues []json.RawMessage
				Errors       []secretError
				NextToken    string
			}
			if err := json.Unmarshal(answer.body, &page); err != nil {
				return awsAnswer{}, &refusal{http.StatusBadGateway, "InternalServiceError", "stage broker: Secrets Manager's answer was unreadable", ""}
			}
			for _, raw := range page.SecretValues {
				var value batchValue
				json.Unmarshal(raw, &value)
				value.raw = raw
				if slices.Contains(names, value.Name) {
					answered[value.Name] = value
				} else {
					q.details = append(q.details, fmt.Sprintf("dropped a value for %s, which was not asked for", value.Name))
				}
			}
			for _, entry := range page.Errors {
				kind, _, _ := strings.Cut(entry.ErrorCode, ":")
				if !awsErrorType.MatchString(kind) {
					kind = "InternalServiceError"
				}
				awsFailed[entry.SecretId] = kind
				q.details = append(q.details, fmt.Sprintf("Secrets Manager answered %s for %s: %s", kind, entry.SecretId, entry.Message))
			}
			if page.NextToken == "" {
				break
			}
			token = page.NextToken
		}
	}
	returned := map[string]bool{}
	for _, secret := range wanted {
		value, found := answered[secret.name]
		switch {
		case found && secret.answeredBy(value.Name, value.ARN):
			if !returned[secret.name] {
				returned[secret.name] = true
				values = append(values, value.raw)
				q.noteRead(secret.name)
			}
		case found:
			addFailure(secret.id, "AccessDeniedException", fmt.Sprintf("secret %s is not listed for this sandbox", shown(secret.id)))
		case awsFailed[secret.name] != "":
			kind := awsFailed[secret.name]
			addFailure(secret.id, kind, fmt.Sprintf("Secrets Manager answered %s for %s; the broker's log on the host has its message", kind, secret.name))
		default:
			addFailure(secret.id, "InternalServiceError", fmt.Sprintf("Secrets Manager did not answer for %s", secret.name))
		}
	}
	encoded, err := json.Marshal(map[string][]json.RawMessage{"SecretValues": values, "Errors": failures})
	return awsAnswer{status: http.StatusOK, body: encoded}, err
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
			found, err := q.listPages(profile, chunk, request.IncludePlannedDeletion)
			if err != nil {
				return awsAnswer{}, err
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

func (q *secretsRequest) listPages(profile string, names []string, includePlannedDeletion *bool) ([]listedEntry, error) {
	var found []listedEntry
	token := ""
	for pages := 0; ; pages++ {
		if pages == maxPages {
			return nil, &refusal{http.StatusBadGateway, "InternalServiceError", fmt.Sprintf("stage broker: Secrets Manager listed more than %d pages", maxPages), ""}
		}
		payload := map[string]any{"Filters": []listFilter{{Key: "name", Values: names}}, "MaxResults": 100}
		if token != "" {
			payload["NextToken"] = token
		}
		if includePlannedDeletion != nil {
			payload["IncludePlannedDeletion"] = *includePlannedDeletion
		}
		answer, err := q.api.call(q.ctx, profile, q.grant.Region, "ListSecrets", strings.Join(names, ", "), payload)
		if err != nil {
			return nil, err
		}
		var page struct {
			SecretList []json.RawMessage
			NextToken  string
		}
		if err := json.Unmarshal(answer.body, &page); err != nil {
			return nil, &refusal{http.StatusBadGateway, "InternalServiceError", "stage broker: Secrets Manager's answer was unreadable", ""}
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
			return found, nil
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

func ServesSecretsManager(url string) bool {
	client := &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Post(url, jsonContentType, strings.NewReader("{}"))
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode == http.StatusUnauthorized && response.Header.Get(BrokerHeader) == brokerHeaderValue
}

func shown(text string) string {
	text = escaped(text)
	if len(text) > shownIDLength {
		return text[:shownIDLength] + "..."
	}
	return text
}

func escaped(text string) string {
	return strings.Trim(strconv.QuoteToASCII(text), `"`)
}
