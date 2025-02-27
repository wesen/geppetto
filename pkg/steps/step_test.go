package steps

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wesen/geppetto/pkg/helpers"
	"testing"
)

func TestSimpleStepInt(t *testing.T) {
	s := NewSimpleStep(func(a int) int {
		return a + 1
	})

	require.Equal(t, SimpleStepNotStarted, s.GetState())

	go func() {
		fmt.Println("Running simple step")
		require.Nil(t, s.Run(context.Background(), 1))
	}()

	fmt.Println("Waiting for output")
	v, ok := <-s.GetOutput()
	require.True(t, ok)
	value, err := v.Value()
	require.Nil(t, err)
	assert.Equal(t, 2, value)
}

func TestSimpleStepAsyncInt(t *testing.T) {
	c := make(chan int)
	s := NewSimpleStep(func(a int) int {
		return <-c + a
	})

	go func() {
		require.Nil(t, s.Run(context.Background(), 1))
	}()
	c <- 1
	v, ok := <-s.GetOutput()
	require.True(t, ok)
	value, err := v.Value()
	require.Nil(t, err)
	assert.Equal(t, 2, value)
}

func TestPipeStepSimple(t *testing.T) {
	s1 := NewSimpleStep(func(a int) int {
		return a + 1
	})
	s2 := NewSimpleStep(func(a int) string {
		return fmt.Sprintf("%d", a+1)
	})
	s := NewPipeStep(s1, s2)

	go func() {
		require.Nil(t, s.Run(context.Background(), 1))
		fmt.Println("Finished running pipe step")
	}()
	v, ok := <-s.GetOutput()
	require.True(t, ok)
	value, err := v.Value()
	require.Nil(t, err)
	assert.Equal(t, "3", value)
}

func TestPipeStepAsync(t *testing.T) {
	// channels need to be buffered so the steps can be started sequentially
	c := make(chan int, 1)
	d := make(chan int, 1)

	s1 := NewSimpleStep(func(a int) helpers.Nothing {
		fmt.Println("s1 start")
		v := <-c
		fmt.Printf("read v: %d\n", v)
		d <- v + a
		return helpers.Nothing{}
	})
	s2 := NewSimpleStep(func(_ helpers.Nothing) string {
		a := <-d
		return fmt.Sprintf("%d", a+1)
	})
	s := NewPipeStep(s1, s2)

	go func() {
		require.Nil(t, s.Run(context.Background(), 1))
	}()
	// probably a race condition now
	c <- 1
	v, ok := <-s.GetOutput()
	require.True(t, ok)
	value, err := v.Value()
	require.Nil(t, err)
	assert.Equal(t, "3", value)
}
