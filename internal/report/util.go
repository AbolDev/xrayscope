package report

import "xrayscope/internal/classify"

func isBackgroundCategory(c string) bool {
	return classify.IsBackground(c) || c == "other" || c == "ip"
}
