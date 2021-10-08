package table

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/kimle/fpl-form/pkg/fpl"
	"github.com/olekukonko/tablewriter"
)

// Table renders the table of in form players
func Table() error {
	m, err := fpl.Elements()
	if err != nil {
		return err
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Position", "Name", "Form", "Transfers in", "Team", "FDR"})
	table.SetAutoMergeCells(true)
	table.SetRowLine(true)
	table.SetAutoWrapText(false)

	keys := make([]int, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	for _, k := range keys {
		for _, e := range m[k] {
			table.Append([]string{
				position(k),
				e.WebName,
				fmt.Sprintf("%.1f", e.Form),
				strconv.Itoa(e.TransfersIn),
				e.Team.Name,
				fdr(e.Difficulties),
			})
		}
	}

	table.Render()
	return nil
}

var position = func(i int) string {
	var s string
	switch i {
	case 1:
		s = "Goalkeepers"
	case 2:
		s = "Defenders"
	case 3:
		s = "Midfielders"
	default:
		s = "Forwards"
	}
	return s
}

func fdr(difficulties []fpl.Difficulty) string {
	sb := strings.Builder{}
	for i, d := range difficulties {
		var loc string
		if d.Home {
			loc = "(A)"
		} else {
			loc = "(H)"
		}
		s := fmt.Sprintf("%s%s:%d, ", d.Name, loc, d.Value)
		if i == len(difficulties)-1 {
			s = strings.TrimRight(s, ", ")
		}
		sb.WriteString(s)
	}
	return sb.String()
}
