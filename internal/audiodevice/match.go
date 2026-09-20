// Package audiodevice contains driver-independent audio-device matching.
package audiodevice

import (
	"fmt"
	"strings"
)

// UniqueMatchIndex returns the only device whose name contains selector after
// case and space normalization. An empty, missing, or ambiguous selector is an
// error so callers never silently choose a different output device.
func UniqueMatchIndex(names []string, selector string) (int, error) {
	want := NormalizeName(selector)
	if want == "" {
		return -1, fmt.Errorf("输出设备选择器不能为空")
	}

	matches := make([]int, 0, 1)
	for i, name := range names {
		if strings.Contains(NormalizeName(name), want) {
			matches = append(matches, i)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return -1, fmt.Errorf("没有找到名字含 %q 的输出设备，当前可用：%s", selector, strings.Join(names, " / "))
	default:
		matched := make([]string, 0, len(matches))
		for _, i := range matches {
			matched = append(matched, names[i])
		}
		return -1, fmt.Errorf("名字含 %q 的输出设备不唯一，请使用更具体的 -device 参数：%s", selector, strings.Join(matched, " / "))
	}
}

// NormalizeName removes ASCII spaces and folds case for the device-name
// substring matching used by both playback and capture selection.
func NormalizeName(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, " ", ""))
}
