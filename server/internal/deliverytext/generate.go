package deliverytext

// The board view shows each delivery mode's rule in the agent's menu; it reads them
// from a file written from ModeRule, so it never words them differently.
//go:generate go run ./moderules -o ../../../web/app/delivery-modes.gen.ts
