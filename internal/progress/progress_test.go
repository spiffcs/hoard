package progress

import (
	"testing"
	"time"
)

func TestEmitOnNilFnIsSilent(t *testing.T) {
	var fn Fn
	fn.Emit(Event{Step: "anything"})
}

func TestMailboxKeepsNewest(t *testing.T) {
	m := NewMailbox()
	fn := m.Fn()
	for i := int64(1); i <= 100; i++ {
		fn(Event{Step: "counting", Done: i, Total: 100})
	}
	select {
	case ev := <-m.C():
		if ev.Done != 100 {
			t.Errorf("received Done=%d, want the newest (100)", ev.Done)
		}
	default:
		t.Fatal("mailbox is empty after 100 sends")
	}
	select {
	case ev := <-m.C():
		t.Errorf("second receive got %+v, want an empty mailbox", ev)
	default:
	}
}

func TestMailboxClose(t *testing.T) {
	m := NewMailbox()
	unblocked := make(chan struct{})
	go func() {
		select {
		case <-m.C():
		case <-m.Done():
		}
		close(unblocked)
	}()
	m.Close()
	select {
	case <-unblocked:
	case <-time.After(time.Second):
		t.Fatal("Close did not release the blocked consumer")
	}
	m.Fn()(Event{Step: "late"})
	m.Close()
}

func TestMailboxProducerNeverBlocks(t *testing.T) {
	m := NewMailbox()
	fn := m.Fn()
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-m.C():
			case <-stop:
				return
			}
		}
	}()
	done := make(chan struct{})
	go func() {
		for i := range int64(10000) {
			fn(Event{Step: "flood", Done: i})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("producer blocked against a consuming reader")
	}
	close(stop)
}

func TestThrottledPassesStepsAndNotesImmediately(t *testing.T) {
	var got []Event
	fn := Throttled(func(ev Event) { got = append(got, ev) }, time.Hour)

	fn(Event{Step: "one", Done: 1})
	fn(Event{Step: "one", Done: 2})
	fn(Event{Step: "one", Done: 3})
	fn(Event{Step: "one", Done: 4, Note: "retrying"})
	fn(Event{Step: "two", Done: 0})

	want := []struct {
		step string
		done int64
	}{{"one", 1}, {"one", 4}, {"two", 0}}
	if len(got) != len(want) {
		t.Fatalf("forwarded %d events, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Step != w.step || got[i].Done != w.done {
			t.Errorf("event %d = %+v, want step %q done %d", i, got[i], w.step, w.done)
		}
	}
}

func TestThrottledForwardsAfterInterval(t *testing.T) {
	var got []Event
	fn := Throttled(func(ev Event) { got = append(got, ev) }, time.Millisecond)
	fn(Event{Step: "s", Done: 1})
	time.Sleep(5 * time.Millisecond)
	fn(Event{Step: "s", Done: 2})
	if len(got) != 2 || got[1].Done != 2 {
		t.Errorf("events = %+v, want the post-interval count forwarded", got)
	}
}

func TestThrottledNilStaysNil(t *testing.T) {
	if Throttled(nil, time.Second) != nil {
		t.Error("Throttled(nil) should stay nil so Emit's nil check keeps working")
	}
}
