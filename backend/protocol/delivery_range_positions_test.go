package protocol

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func measureDeliveryUTF16(text string) (int64, error) { return int64(utf16TextLength(text)), nil }

func TestDeliveryTextPositionsKeepOnlyUnchangedRegions(t *testing.T) {
	positions := deliveryTextPositions{}
	if err := positions.append("head ", "head ", measureDeliveryUTF16); err != nil {
		t.Fatal(err)
	}
	if positions.diverged || len(positions.spans) != 0 {
		t.Fatal("unchanged appends materialized an interval table")
	}
	if err := positions.append("😀abCDxy", "😀abQxy", measureDeliveryUTF16); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		start, end, wantStart, wantEnd int64
		valid                          bool
	}{
		{0, 9, 0, 9, true}, {9, 11, 0, 0, false}, {11, 13, 10, 12, true},
		{8, 12, 0, 0, false}, {0, 14, 0, 0, false}, {-1, 1, 0, 0, false},
	} {
		start, end, err := positions.project(test.start, test.end)
		if (err == nil) != test.valid || err == nil && (start != test.wantStart || end != test.wantEnd) {
			t.Fatal(test, start, end, err)
		}
	}
	for range 1024 {
		if err := positions.append("z", "z", measureDeliveryUTF16); err != nil {
			t.Fatal(err)
		}
	}
	if len(positions.spans) != 2 || positions.spans[1].length != 1026 {
		t.Fatal("adjacent clean increments did not coalesce", positions)
	}
	var equalLength deliveryTextPositions
	if err := equalLength.append("a😀z", "a😁z", measureDeliveryUTF16); err != nil {
		t.Fatal(err)
	}
	if _, _, err := equalLength.project(1, 3); err == nil {
		t.Fatal("equal lengths or shared UTF-8 bytes proved a changed character")
	}
}

func FuzzDeliveryUnchangedIntervals(f *testing.F) {
	for _, input := range [][2]string{{"😀abCDxy", "😀abQxy"}, {"abc", "axc"}, {"", "new"}, {"same", "same"}} {
		f.Add(input[0], input[1])
	}
	f.Fuzz(func(t *testing.T, before, after string) {
		if !utf8.ValidString(before) || !utf8.ValidString(after) || len(before)+len(after) > 1024 {
			t.Skip()
		}
		positions := deliveryTextPositions{}
		if err := positions.append(before, after, measureDeliveryUTF16); err != nil {
			t.Fatal(err)
		}
		left, right := []rune(before), []rune(after)
		prefix := 0
		for prefix < len(left) && prefix < len(right) && left[prefix] == right[prefix] {
			prefix++
		}
		suffix := 0
		for suffix < len(left)-prefix && suffix < len(right)-prefix && left[len(left)-suffix-1] == right[len(right)-suffix-1] {
			suffix++
		}
		prefixUnits := int64(utf16TextLength(string(left[:prefix])))
		suffixStart := int64(utf16TextLength(string(left[:len(left)-suffix])))
		shift := int64(utf16TextLength(after) - utf16TextLength(before))
		// Check all character-sized intervals and ranges crossing the unknown
		// region against a deliberately simple rune oracle.
		start := int64(0)
		for index, r := range left {
			width := int64(1)
			if r > 0xffff {
				width = 2
			}
			end := start + width
			wantValid := before == after || end <= prefixUnits || start >= suffixStart
			gotStart, gotEnd, err := positions.project(start, end)
			if (err == nil) != wantValid {
				t.Fatal(index, before, after, start, end, err)
			}
			if err == nil {
				offset := int64(0)
				if start >= suffixStart && end > prefixUnits {
					offset = shift
				}
				if gotStart != start+offset || gotEnd != end+offset {
					t.Fatal("incorrect translation")
				}
			}
			start = end
		}
		if before != after && prefixUnits < suffixStart {
			if _, _, err := positions.project(0, positions.source); err == nil && strings.Compare(before, after) != 0 {
				t.Fatal("rewritten region was bridged")
			}
		}
	})
}
