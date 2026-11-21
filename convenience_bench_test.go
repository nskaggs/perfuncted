package perfuncted

import (
	"context"
	"testing"
)

func BenchmarkConvenienceBatch(b *testing.B) {
	s := NewSessionForTesting(nil, &closeCountingInput{}, nil, nil, nil)
	defer s.Close()
	steps := make([]Step, 10)
	for i := range steps {
		steps[i] = Type("example")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Do(steps...); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConvenienceDirect(b *testing.B) {
	s := NewSessionForTesting(nil, &closeCountingInput{}, nil, nil, nil)
	defer s.Close()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 10; j++ {
			if err := s.Input.TypeLiteral(ctx, "example"); err != nil {
				b.Fatal(err)
			}
		}
	}
}
