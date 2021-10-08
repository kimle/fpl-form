package fpl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiURL      = "https://fantasy.premierleague.com/api/bootstrap-static/"
	fixturesURL = "https://fantasy.premierleague.com/api/fixtures/"
	userAgent   = "Mozilla/5.0 (X11; Fedora; Linux x86_64; rv:85.0) Gecko/20100101 Firefox/88.0"
	path        = "/tmp/fpl"
)

type apiRequest struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
	wg         *sync.WaitGroup
	client     *http.Client
	ch         chan map[string][]byte
	errc       chan error
}

type apiResponse struct {
	Elements     []Element `json:"elements"`
	Teams        []Team    `json:"teams"`
	Fixtures     []Fixture `json:"fixtures"`
	CurrentEvent int64
}

// Fixture is a struct containing team difficulties
type Fixture struct {
	Gameweek           json.Number `json:"event"`
	TeamAway           json.Number `json:"team_a"`
	TeamHome           json.Number `json:"team_h"`
	TeamHomeDifficulty json.Number `json:"team_h_difficulty"`
	TeamAwayDifficulty json.Number `json:"team_a_difficulty"`
}

// Difficulty contains the five upcoming fixture difficulties for the player
type Difficulty struct {
	Name  string
	Value int
	Home  bool
}

// Team is a struct containing team ID and name
type Team struct {
	ID   int
	Name string
}

// Element is a simplified struct containing only the relevant fields for a player
type Element struct {
	Form         float32
	Type         int
	LastName     string
	FirstName    string
	WebName      string
	TransfersIn  int
	Team         Team
	Difficulties []Difficulty
}

type event struct {
	ID     json.Number `json:"id"`
	IsNext bool        `json:"is_next"`
}

// Elements makes a request to FPL API and deserializes player data to a slice of Element
func Elements() (map[int][]Element, error) {
	var (
		ar         *apiResponse
		filesExist bool
	)

	if err := os.MkdirAll(path, os.ModePerm); err != nil {
		return nil, err
	}

	if err := clean(); err != nil {
		return nil, err
	}

	files, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		if file.Name() == filename() || file.Name() == fixturesFilename() {
			filesExist = true
		} else {
			filesExist = false
		}
	}
	if filesExist {
		ar, err = read()
	} else {
		ar, err = request()
	}
	ar.nextFixtures()

	m := make(map[int][]Element, 4)

	for _, element := range ar.Elements {
		if v, ok := m[element.Type]; ok {
			v = append(v, element)
			m[element.Type] = v
		} else {
			m[element.Type] = make([]Element, 0)
		}
	}

	for k, v := range m {
		sort.SliceStable(v, func(i, j int) bool { return v[i].Form > v[j].Form })
		v = v[:5]
		for i, e := range v {
			e.difficulties(ar.Teams, ar.Fixtures)
			v[i] = e
		}
		m[k] = v
	}
	return m, nil
}

// UnmarshalJSON implements json.Unmarshaler interface
func (ar *apiResponse) UnmarshalJSON(b []byte) error {
	var (
		m        map[string]json.RawMessage
		teams    []Team
		elements []Element
		events   []event
		sb       strings.Builder
	)

	es := func(err error) string { return fmt.Sprintf("%s\n", err.Error()) }

	if err := json.Unmarshal(b, &m); err != nil {
		sb.WriteString(es(err))
	}

	if err := json.Unmarshal(m["teams"], &teams); err != nil {
		sb.WriteString(es(err))
	}
	ar.Teams = teams

	if err := json.Unmarshal(m["elements"], &elements); err != nil {
		sb.WriteString(es(err))
	}

	if err := json.Unmarshal(m["events"], &events); err != nil {
		sb.WriteString((es(err)))
	}

	for _, event := range events {
		if event.IsNext {
			i, _ := event.ID.Int64()
			ar.CurrentEvent = i
		}
	}

	if sb.Len() > 0 {
		return errors.New(sb.String())
	}

	tm := ar.teams()
	for i, e := range elements {
		t := Team{ID: e.Team.ID, Name: tm[e.Team.ID]}
		e.Team = t
		elements[i] = e
	}

	ar.Elements = elements

	return nil
}

// UnmarshalJSON implements json.Unmarshaler interface
func (t *Team) UnmarshalJSON(b []byte) error {
	var m map[string]interface{}
	err := json.Unmarshal(b, &m)
	if err != nil {
		return err
	}
	for k, v := range m {
		switch k {
		case "id":
			t.ID = int(v.(float64))
		case "short_name":
			t.Name = v.(string)
		}
	}
	return nil
}

// UnmarshalJSON implements json.Unmarshaler interface
func (e *Element) UnmarshalJSON(b []byte) error {
	var m map[string]interface{}
	err := json.Unmarshal(b, &m)
	if err != nil {
		return err
	}
	for k, v := range m {
		switch k {
		case "first_name":
			e.FirstName = v.(string)
		case "second_name":
			e.LastName = v.(string)
		case "web_name":
			e.WebName = v.(string)
		case "form":
			f, err := strconv.ParseFloat(v.(string), 32)
			if err != nil {
				return err
			}
			e.Form = float32(f)
		case "element_type":
			e.Type = int(v.(float64))
		case "transfers_in_event":
			e.TransfersIn = int(v.(float64))
		case "team":
			e.Team = Team{ID: int(v.(float64))}
		}
	}
	return nil
}

func (ar apiResponse) teams() map[int]string {
	teams := make(map[int]string, len(ar.Teams))
	for _, team := range ar.Teams {
		teams[team.ID] = team.Name
	}
	return teams
}

func request() (*apiResponse, error) {
	var (
		wg       sync.WaitGroup
		fixtures []Fixture
		apiResp  apiResponse
		errs     []error
		sb       strings.Builder
		wrapped  error
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := &http.Client{}
	wg.Add(2)

	ch := make(chan map[string][]byte, 2)
	errc := make(chan error, 2)

	apiReq := apiRequest{ctx, cancel, &wg, client, ch, errc}
	go apiReq.fetch(fixturesURL, fixturesFilename())
	go apiReq.fetch(apiURL, filename())

	cwg := &sync.WaitGroup{}
	cwg.Add(2)

	go func(cwg *sync.WaitGroup) {
		for m := range ch {
			if v, ok := m["api"]; ok {
				if err := json.Unmarshal(v, &apiResp); err != nil {
					cancel()
					errs = append(errs, err)
				}
			} else if v, ok := m["fixtures"]; ok {
				if err := json.Unmarshal(v, &fixtures); err != nil {
					cancel()
					errs = append(errs, err)
				}
			}
		}
		cwg.Done()
	}(cwg)

	go func(cwg *sync.WaitGroup) {
		for err := range errc {
			errs = append(errs, err)
		}

		es := func(err error) string { return fmt.Sprintf("%s\n", err.Error()) }

		for _, e := range errs {
			sb.WriteString(es(e))
		}
		if sb.Len() > 0 {
			wrapped = errors.New(sb.String())
		}

		cwg.Done()
	}(cwg)

	wg.Wait()

	close(ch)
	close(errc)

	cwg.Wait()

	apiResp.Fixtures = fixtures

	return &apiResp, wrapped
}

func (ar *apiRequest) fetch(url string, filename string) {
	defer ar.wg.Done()
	req, err := http.NewRequestWithContext(ar.ctx, http.MethodGet, url, nil)
	if err != nil {
		ar.errc <- err
		ar.cancelFunc()
		return
	}

	req.Header.Set("user-agent", userAgent)
	resp, err := ar.client.Do(req)
	if err != nil {
		ar.errc <- err
		ar.cancelFunc()
		return
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		ar.errc <- err
		ar.cancelFunc()
		return
	}

	errc := make(chan error, 1)
	go save(body, filename, errc)

	m := make(map[string][]byte, 1)
	if url == apiURL {
		m["api"] = body
	} else {
		m["fixtures"] = body
	}
	ar.ch <- m

	for err := range errc {
		if err != nil {
			log.Printf("could not save file %s: %+v\n", filename, err)
		}
	}
}

func save(b []byte, filename string, errc chan error) {
	if err := os.Chdir(path); err != nil {
		errc <- err
		return
	}
	if err := ioutil.WriteFile(filename, b, os.ModePerm); err != nil {
		errc <- err
		return
	}
	close(errc)
}

func read() (*apiResponse, error) {
	if err := clean(); err != nil {
		return nil, err
	}

	if err := os.Chdir(path); err != nil {
		return nil, err

	}

	files, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var (
		ar       apiResponse
		fixtures []Fixture
	)

	for _, file := range files {
		b, err := ioutil.ReadFile(file.Name())
		if err != nil {
			return nil, err
		}
		if file.Name() == filename() {
			if err := json.Unmarshal(b, &ar); err != nil {
				return nil, err
			}
		} else if file.Name() == fixturesFilename() {
			if err := json.Unmarshal(b, &fixtures); err != nil {
				return nil, err
			}
		}
	}

	ar.Fixtures = fixtures

	return &ar, nil
}

func filename() string {
	date := strings.Split(time.Now().Local().String(), " ")[0]
	return fmt.Sprintf("%s.json", date)
}

func fixturesFilename() string {
	date := strings.Split(time.Now().Local().String(), " ")[0]
	return fmt.Sprintf("%s-fixtures.json", date)
}

func clean() error {
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}
	err = os.Chdir(path)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.Name() != filename() && file.Name() != fixturesFilename() {
			err = os.Remove(file.Name())
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (ar *apiResponse) nextFixtures() {
	fixtures := make([]Fixture, 0)
	for _, f := range ar.Fixtures {
		i, _ := f.Gameweek.Int64()
		if i < ar.CurrentEvent+5 && i >= ar.CurrentEvent {
			fixtures = append(fixtures, f)
		}
	}
	ar.Fixtures = fixtures
}

func (e *Element) difficulties(teams []Team, fixtures []Fixture) {
	difficulties := make([]Difficulty, 0, 15)
	for _, f := range fixtures {
		var (
			t    *Team
			d    int64
			home bool
		)
		ta, _ := f.TeamAway.Int64()
		th, _ := f.TeamHome.Int64()
		if e.Team.ID == int(ta) {
			t = team(th, teams)
			if t == nil {
				log.Println("team home is nil")
			}
			d, _ = f.TeamHomeDifficulty.Int64()
			home = false
		} else if e.Team.ID == int(th) {
			t = team(ta, teams)
			if t == nil {
				log.Println("team away is nil")
			}
			d, _ = f.TeamAwayDifficulty.Int64()
			home = true
		} else {
			continue
		}
		difficulties = append(difficulties, Difficulty{t.Name, int(d), home})
	}
	e.Difficulties = difficulties
}

func team(ID int64, teams []Team) *Team {
	for _, team := range teams {
		if int(ID) == team.ID {
			return &team
		}
	}
	return nil
}
