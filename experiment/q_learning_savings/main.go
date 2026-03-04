package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
)

const (
	actionWait = iota
	actionTriggerGC
	numActions = 2
)

const mebibyte = 1024.0 * 1024.0

type Config struct {
	Seed          int64             `json:"seed"`
	TrainEpisodes int               `json:"trainEpisodes"`
	EvalEpisodes  int               `json:"evalEpisodes"`
	Horizon       int               `json:"horizon"`
	EMAAlpha      float64           `json:"emaAlpha"`
	Epsilon       float64           `json:"epsilon"`
	Discretizer   DiscretizerConfig `json:"discretizer"`
	Learning      LearningConfig    `json:"learning"`
	Cost          CostConfig        `json:"cost"`
	Environment   EnvironmentConfig `json:"environment"`
	GC            GCConfig          `json:"gc"`
	Baseline      BaselineConfig    `json:"baseline"`
	Safety        SafetyGateConfig  `json:"safetyGate"`
	Output        OutputConfig      `json:"output"`
	configDir     string
}

type DiscretizerConfig struct {
	Tau1 []float64 `json:"tau1"`
	Tau2 []float64 `json:"tau2"`
	Tau3 []float64 `json:"tau3"`
	Tau4 []float64 `json:"tau4"`
	Tau5 []float64 `json:"tau5"`
}

type LearningConfig struct {
	Eta          float64 `json:"eta"`
	Gamma        float64 `json:"gamma"`
	EpsilonStart float64 `json:"epsilonStart"`
	EpsilonMin   float64 `json:"epsilonMin"`
	EpsilonDecay float64 `json:"epsilonDecay"`
}

type CostConfig struct {
	WS                    float64 `json:"wS"`
	WG                    float64 `json:"wG"`
	WR                    float64 `json:"wR"`
	StorePricePerByte     float64 `json:"storePricePerByte"`
	ComputePricePerSec    float64 `json:"computePricePerSec"`
	RequestPrice          float64 `json:"requestPrice"`
	BandwidthPricePerByte float64 `json:"bandwidthPricePerByte"`
	ScanPricePerByte      float64 `json:"scanPricePerByte"`
}

type EnvironmentConfig struct {
	InitialLiveBytes             float64 `json:"initialLiveBytes"`
	InitialGarbageBytes          float64 `json:"initialGarbageBytes"`
	IngestBandwidthBytesPerStep  float64 `json:"ingestBandwidthBytesPerStep"`
	WriteBytesMin                float64 `json:"writeBytesMin"`
	WriteBytesMax                float64 `json:"writeBytesMax"`
	ReadBytesMin                 float64 `json:"readBytesMin"`
	ReadBytesMax                 float64 `json:"readBytesMax"`
	InvalidationRatioMin         float64 `json:"invalidationRatioMin"`
	InvalidationRatioMax         float64 `json:"invalidationRatioMax"`
	RemoteScanBaseMin            float64 `json:"remoteScanBaseMin"`
	RemoteScanBaseMax            float64 `json:"remoteScanBaseMax"`
	RemoteScanRhoFactor          float64 `json:"remoteScanRhoFactor"`
	WorkloadShockProbability     float64 `json:"workloadShockProbability"`
	WorkloadShockWriteMultiplier float64 `json:"workloadShockWriteMultiplier"`
	WorkloadShockScanMultiplier  float64 `json:"workloadShockScanMultiplier"`
	ScanAmplificationByRho       float64 `json:"scanAmplificationByRho"`
}

type GCConfig struct {
	BudgetPerStep         float64 `json:"budgetPerStep"`
	WindowH               float64 `json:"windowH"`
	CandidateCountMin     int     `json:"candidateCountMin"`
	CandidateCountMax     int     `json:"candidateCountMax"`
	CandidateSizeMinBytes float64 `json:"candidateSizeMinBytes"`
	CandidateSizeMaxBytes float64 `json:"candidateSizeMaxBytes"`
	GarbageRatioNoiseStd  float64 `json:"garbageRatioNoiseStd"`
	DurationPerMB         float64 `json:"durationPerMB"`
	DurationNoiseStd      float64 `json:"durationNoiseStd"`
	RequestPerMB          float64 `json:"requestPerMB"`
	RequestNoiseStd       float64 `json:"requestNoiseStd"`
	TransferMultiplier    float64 `json:"transferMultiplier"`
	TransferNoiseStd      float64 `json:"transferNoiseStd"`
	ScanBenefitPerGarbage float64 `json:"scanBenefitPerGarbage"`
	ScanBenefitNoiseStd   float64 `json:"scanBenefitNoiseStd"`
}

type BaselineConfig struct {
	RhoThreshold  float64 `json:"rhoThreshold"`
	CHatThreshold float64 `json:"cHatThreshold"`
	RHatThreshold float64 `json:"rHatThreshold"`
}

type SafetyGateConfig struct {
	Enabled      bool    `json:"enabled"`
	DeltaE       float64 `json:"deltaE"`
	DeltaB       float64 `json:"deltaB"`
	EmergencyRho float64 `json:"emergencyRho"`
}

type OutputConfig struct {
	PerEpisode bool `json:"perEpisode"`
}

type Observation struct {
	Rho             float64
	CHat            float64
	WHat            float64
	SHat            float64
	RHat            float64
	DeltaStoreCost  float64
	BaseScanCost    float64
	WriteBytes      float64
	ReadBytes       float64
	RemoteScanBytes float64
	Candidates      []Candidate
}

type Candidate struct {
	SizeBytes     float64
	GarbageRatio  float64
	GarbageBytes  float64
	DurationSec   float64
	RequestCount  float64
	TransferBytes float64
	StoreBenefit  float64
	ScanBenefit   float64
	GCCost        float64
	NetBenefit    float64
	ROI           float64
}

type StepResult struct {
	Observation Observation
	Reward      float64
	Cost        CostBreakdown
	Action      int
}

type CostBreakdown struct {
	Store float64
	GC    float64
	Scan  float64
	Total float64
}

type EpisodeStats struct {
	Cost   CostBreakdown
	Reward float64
}

type EvalSummary struct {
	QMeanCost        float64
	BaselineMeanCost float64
	MeanSavings      float64
	MeanSavingsPct   float64
	PSavings         map[string]float64
	QMeanStore       float64
	QMeanGC          float64
	QMeanScan        float64
	BMeanStore       float64
	BMeanGC          float64
	BMeanScan        float64
}

type Encoder struct {
	thresholds [5][]float64
	bins       [5]int
	stateCount int
}

func NewEncoder(cfg DiscretizerConfig) (*Encoder, error) {
	e := &Encoder{
		thresholds: [5][]float64{cfg.Tau1, cfg.Tau2, cfg.Tau3, cfg.Tau4, cfg.Tau5},
	}
	for i := 0; i < 5; i++ {
		if len(e.thresholds[i]) == 0 {
			return nil, fmt.Errorf("discretizer tau%d must not be empty", i+1)
		}
		if !sort.Float64sAreSorted(e.thresholds[i]) {
			return nil, fmt.Errorf("discretizer tau%d must be sorted ascending", i+1)
		}
		e.bins[i] = len(e.thresholds[i]) + 1
	}
	prod := 1
	for _, b := range e.bins {
		prod *= b
	}
	e.stateCount = prod
	return e, nil
}

func bucket(v float64, thresholds []float64) int {
	count := 0
	for _, th := range thresholds {
		if v >= th {
			count++
		}
	}
	return count
}

func (e *Encoder) Encode(obs Observation) int {
	vals := [5]float64{obs.Rho, obs.CHat, obs.WHat, obs.SHat, obs.RHat}
	idx := 0
	mult := 1
	for i := 0; i < 5; i++ {
		b := bucket(vals[i], e.thresholds[i])
		idx += b * mult
		mult *= e.bins[i]
	}
	return idx
}

func (e *Encoder) StateCount() int { return e.stateCount }

type Agent struct {
	q        [][]float64
	learning LearningConfig
	rng      *rand.Rand
	epsilon  float64
}

func NewAgent(stateCount int, learning LearningConfig, seed int64) *Agent {
	q := make([][]float64, stateCount)
	for i := range q {
		q[i] = make([]float64, numActions)
	}
	return &Agent{
		q:        q,
		learning: learning,
		rng:      rand.New(rand.NewSource(seed)),
		epsilon:  learning.EpsilonStart,
	}
}

func (a *Agent) GreedyAction(state int) int {
	if a.q[state][actionTriggerGC] > a.q[state][actionWait] {
		return actionTriggerGC
	}
	return actionWait
}

func (a *Agent) SelectAction(state int) int {
	if a.rng.Float64() < a.epsilon {
		if a.rng.Intn(2) == 0 {
			return actionWait
		}
		return actionTriggerGC
	}
	return a.GreedyAction(state)
}

func (a *Agent) Update(s, action int, reward float64, sNext int) {
	bestNext := a.q[sNext][actionWait]
	if a.q[sNext][actionTriggerGC] > bestNext {
		bestNext = a.q[sNext][actionTriggerGC]
	}
	target := reward + a.learning.Gamma*bestNext
	a.q[s][action] = (1.0-a.learning.Eta)*a.q[s][action] + a.learning.Eta*target
}

func (a *Agent) DecayEpsilon() {
	a.epsilon = math.Max(a.learning.EpsilonMin, a.epsilon*a.learning.EpsilonDecay)
}

type Simulator struct {
	cfg          Config
	cost         CostConfig
	env          EnvironmentConfig
	gc           GCConfig
	ema          float64
	liveBytes    float64
	garbageBytes float64
	rng          *rand.Rand
}

func NewSimulator(cfg Config, seed int64) *Simulator {
	s := &Simulator{
		cfg:  cfg,
		cost: cfg.Cost,
		env:  cfg.Environment,
		gc:   cfg.GC,
		rng:  rand.New(rand.NewSource(seed)),
	}
	s.Reset()
	return s
}

func (s *Simulator) Reset() Observation {
	s.liveBytes = s.env.InitialLiveBytes
	s.garbageBytes = s.env.InitialGarbageBytes
	deltaStore := s.cost.StorePricePerByte * (s.liveBytes + s.garbageBytes)
	s.ema = deltaStore
	obs := s.buildObservation()
	return obs
}

func (s *Simulator) Step(action int) StepResult {
	writeBytes := s.sampleUniform(s.env.WriteBytesMin, s.env.WriteBytesMax)
	readBytes := s.sampleUniform(s.env.ReadBytesMin, s.env.ReadBytesMax)
	invalidRatio := s.sampleUniform(s.env.InvalidationRatioMin, s.env.InvalidationRatioMax)
	baseScanRatio := s.sampleUniform(s.env.RemoteScanBaseMin, s.env.RemoteScanBaseMax)

	if s.rng.Float64() < s.env.WorkloadShockProbability {
		writeBytes *= s.env.WorkloadShockWriteMultiplier
		baseScanRatio *= s.env.WorkloadShockScanMultiplier
	}

	invalidated := writeBytes * invalidRatio
	if invalidated > s.liveBytes {
		invalidated = s.liveBytes
	}
	s.liveBytes = s.liveBytes + writeBytes - invalidated
	s.garbageBytes += invalidated

	rho := safeRatio(s.garbageBytes, s.liveBytes+s.garbageBytes, s.cfg.Epsilon)
	remoteScanRatio := baseScanRatio + s.env.RemoteScanRhoFactor*rho
	if remoteScanRatio < 0 {
		remoteScanRatio = 0
	}
	remoteScanBytes := remoteScanRatio * readBytes

	candidates := s.sampleCandidates(rho)

	reclaimedGarbage := 0.0
	gcCost := 0.0
	scanBenefit := 0.0
	if action == actionTriggerGC {
		selected := selectByROI(candidates, s.gc.BudgetPerStep)
		for _, c := range selected {
			reclaimedGarbage += c.GarbageBytes
			gcCost += c.GCCost
			scanBenefit += c.ScanBenefit
		}
		if reclaimedGarbage > s.garbageBytes {
			reclaimedGarbage = s.garbageBytes
		}
		s.garbageBytes -= reclaimedGarbage
	}

	rhoAfter := safeRatio(s.garbageBytes, s.liveBytes+s.garbageBytes, s.cfg.Epsilon)
	storeCost := s.cost.StorePricePerByte * (s.liveBytes + s.garbageBytes)
	scanCostBase := s.cost.ScanPricePerByte * remoteScanBytes * (1.0 + s.env.ScanAmplificationByRho*rhoAfter)
	scanCost := math.Max(0, scanCostBase-scanBenefit)
	totalCost := s.cost.WS*storeCost + s.cost.WG*gcCost + s.cost.WR*scanCost
	reward := -totalCost

	deltaStore := storeCost
	s.ema = s.cfg.EMAAlpha*deltaStore + (1.0-s.cfg.EMAAlpha)*s.ema

	nextObs := s.buildObservationWithIO(writeBytes, readBytes, remoteScanBytes)
	nextObs.Candidates = s.sampleCandidates(nextObs.Rho)
	nextObs.RHat = p75ROI(nextObs.Candidates)
	nextObs.DeltaStoreCost = storeCost
	nextObs.BaseScanCost = scanCostBase

	return StepResult{
		Observation: nextObs,
		Reward:      reward,
		Action:      action,
		Cost: CostBreakdown{
			Store: storeCost,
			GC:    gcCost,
			Scan:  scanCost,
			Total: totalCost,
		},
	}
}

func (s *Simulator) buildObservation() Observation {
	writeBytes := s.sampleUniform(s.env.WriteBytesMin, s.env.WriteBytesMax)
	readBytes := s.sampleUniform(s.env.ReadBytesMin, s.env.ReadBytesMax)
	rho := safeRatio(s.garbageBytes, s.liveBytes+s.garbageBytes, s.cfg.Epsilon)
	remoteScanRatio := s.sampleUniform(s.env.RemoteScanBaseMin, s.env.RemoteScanBaseMax) + s.env.RemoteScanRhoFactor*rho
	if remoteScanRatio < 0 {
		remoteScanRatio = 0
	}
	remoteScanBytes := remoteScanRatio * readBytes
	candidates := s.sampleCandidates(rho)
	obs := s.makeObservation(writeBytes, readBytes, remoteScanBytes, candidates)
	return obs
}

func (s *Simulator) buildObservationWithIO(writeBytes, readBytes, remoteScanBytes float64) Observation {
	candidates := []Candidate{}
	obs := s.makeObservation(writeBytes, readBytes, remoteScanBytes, candidates)
	return obs
}

func (s *Simulator) makeObservation(writeBytes, readBytes, remoteScanBytes float64, candidates []Candidate) Observation {
	rho := safeRatio(s.garbageBytes, s.liveBytes+s.garbageBytes, s.cfg.Epsilon)
	deltaStore := s.cost.StorePricePerByte * (s.liveBytes + s.garbageBytes)
	cHat := deltaStore / math.Max(s.cfg.Epsilon, s.ema)
	wHat := writeBytes / math.Max(s.cfg.Epsilon, s.env.IngestBandwidthBytesPerStep)
	sHat := remoteScanBytes / math.Max(s.cfg.Epsilon, readBytes)
	rHat := p75ROI(candidates)
	scanBase := s.cost.ScanPricePerByte * remoteScanBytes * (1.0 + s.env.ScanAmplificationByRho*rho)
	return Observation{
		Rho:             rho,
		CHat:            cHat,
		WHat:            wHat,
		SHat:            sHat,
		RHat:            rHat,
		DeltaStoreCost:  deltaStore,
		BaseScanCost:    scanBase,
		WriteBytes:      writeBytes,
		ReadBytes:       readBytes,
		RemoteScanBytes: remoteScanBytes,
		Candidates:      candidates,
	}
}

func (s *Simulator) sampleCandidates(globalRho float64) []Candidate {
	count := s.gc.CandidateCountMin
	if s.gc.CandidateCountMax > s.gc.CandidateCountMin {
		count += s.rng.Intn(s.gc.CandidateCountMax - s.gc.CandidateCountMin + 1)
	}
	out := make([]Candidate, 0, count)
	for i := 0; i < count; i++ {
		size := s.sampleUniform(s.gc.CandidateSizeMinBytes, s.gc.CandidateSizeMaxBytes)
		fileRho := clamp(globalRho+s.rng.NormFloat64()*s.gc.GarbageRatioNoiseStd, 0, 1)
		garbageBytes := fileRho * size

		sizeMB := size / mebibyte
		duration := math.Max(0.001, s.gc.DurationPerMB*sizeMB*(1.0+s.rng.NormFloat64()*s.gc.DurationNoiseStd))
		req := math.Max(1.0, s.gc.RequestPerMB*sizeMB*(1.0+s.rng.NormFloat64()*s.gc.RequestNoiseStd))
		transfer := math.Max(0, size*s.gc.TransferMultiplier*(1.0+s.rng.NormFloat64()*s.gc.TransferNoiseStd))

		storeBenefit := s.cost.StorePricePerByte * garbageBytes * s.gc.WindowH
		scanBenefit := math.Max(0, s.gc.ScanBenefitPerGarbage*garbageBytes*(1.0+s.rng.NormFloat64()*s.gc.ScanBenefitNoiseStd))
		gcCost := s.cost.ComputePricePerSec*duration + s.cost.RequestPrice*req + s.cost.BandwidthPricePerByte*transfer
		net := storeBenefit + scanBenefit - gcCost
		roi := net / math.Max(s.cfg.Epsilon, gcCost)
		out = append(out, Candidate{
			SizeBytes:     size,
			GarbageRatio:  fileRho,
			GarbageBytes:  garbageBytes,
			DurationSec:   duration,
			RequestCount:  req,
			TransferBytes: transfer,
			StoreBenefit:  storeBenefit,
			ScanBenefit:   scanBenefit,
			GCCost:        gcCost,
			NetBenefit:    net,
			ROI:           roi,
		})
	}
	return out
}

func selectByROI(candidates []Candidate, budget float64) []Candidate {
	filtered := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if c.NetBenefit > 0 {
			filtered = append(filtered, c)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].ROI == filtered[j].ROI {
			return filtered[i].GCCost < filtered[j].GCCost
		}
		return filtered[i].ROI > filtered[j].ROI
	})
	selected := make([]Candidate, 0, len(filtered))
	used := 0.0
	for _, c := range filtered {
		if used+c.GCCost > budget {
			continue
		}
		selected = append(selected, c)
		used += c.GCCost
	}
	return selected
}

func p75ROI(cands []Candidate) float64 {
	if len(cands) == 0 {
		return 0
	}
	vals := make([]float64, len(cands))
	for i := range cands {
		vals[i] = cands[i].ROI
	}
	sort.Float64s(vals)
	idx := int(math.Ceil(0.75*float64(len(vals)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(vals) {
		idx = len(vals) - 1
	}
	return vals[idx]
}

func runTraining(cfg Config, enc *Encoder, agent *Agent) {
	for ep := 0; ep < cfg.TrainEpisodes; ep++ {
		sim := NewSimulator(cfg, cfg.Seed+int64(ep)*7919)
		obs := sim.Reset()
		state := enc.Encode(obs)
		for step := 0; step < cfg.Horizon; step++ {
			action := agent.SelectAction(state)
			action = applySafetyGate(cfg.Safety, obs, action)
			res := sim.Step(action)
			nextState := enc.Encode(res.Observation)
			agent.Update(state, action, res.Reward, nextState)
			agent.DecayEpsilon()
			state = nextState
			obs = res.Observation
		}
	}
}

func runEpisodeWithPolicy(cfg Config, enc *Encoder, seed int64, policy func(Observation, int) int, episodeID int, title string) EpisodeStats {
	sim := NewSimulator(cfg, seed)
	obs := sim.Reset()
	state := enc.Encode(obs)
	stats := EpisodeStats{}
	for step := 0; step < cfg.Horizon; step++ {
		action := policy(obs, state)
		action = applySafetyGate(cfg.Safety, obs, action)
		res := sim.Step(action)
		stats.Cost.Store += res.Cost.Store
		stats.Cost.GC += res.Cost.GC
		stats.Cost.Scan += res.Cost.Scan
		stats.Cost.Total += res.Cost.Total
		stats.Reward += res.Reward
		obs = res.Observation
		state = enc.Encode(obs)
	}
	if cfg.Output.PerEpisode {
		fmt.Printf("[%s][episode=%d] total=%.6f store=%.6f gc=%.6f scan=%.6f\n",
			title, episodeID, stats.Cost.Total, stats.Cost.Store, stats.Cost.GC, stats.Cost.Scan)
	}
	return stats
}

func runEvaluation(cfg Config, enc *Encoder, agent *Agent) EvalSummary {
	qCosts := make([]float64, 0, cfg.EvalEpisodes)
	bCosts := make([]float64, 0, cfg.EvalEpisodes)
	savings := make([]float64, 0, cfg.EvalEpisodes)

	qStore, qGC, qScan := 0.0, 0.0, 0.0
	bStore, bGC, bScan := 0.0, 0.0, 0.0

	qPolicy := func(_ Observation, state int) int {
		return agent.GreedyAction(state)
	}
	baselinePolicy := func(obs Observation, _ int) int {
		if obs.Rho >= cfg.Baseline.RhoThreshold &&
			(obs.CHat >= cfg.Baseline.CHatThreshold || obs.RHat >= cfg.Baseline.RHatThreshold) {
			return actionTriggerGC
		}
		return actionWait
	}

	for ep := 0; ep < cfg.EvalEpisodes; ep++ {
		seed := cfg.Seed + 100000 + int64(ep)*3571
		qStats := runEpisodeWithPolicy(cfg, enc, seed, qPolicy, ep, "Q")
		bStats := runEpisodeWithPolicy(cfg, enc, seed, baselinePolicy, ep, "BASE")

		qCosts = append(qCosts, qStats.Cost.Total)
		bCosts = append(bCosts, bStats.Cost.Total)
		savings = append(savings, bStats.Cost.Total-qStats.Cost.Total)

		qStore += qStats.Cost.Store
		qGC += qStats.Cost.GC
		qScan += qStats.Cost.Scan
		bStore += bStats.Cost.Store
		bGC += bStats.Cost.GC
		bScan += bStats.Cost.Scan
	}

	qMean := mean(qCosts)
	bMean := mean(bCosts)
	savingMean := mean(savings)
	savingPct := 0.0
	if bMean > 0 {
		savingPct = savingMean / bMean
	}

	return EvalSummary{
		QMeanCost:        qMean,
		BaselineMeanCost: bMean,
		MeanSavings:      savingMean,
		MeanSavingsPct:   savingPct,
		PSavings: map[string]float64{
			"p50": percentile(savings, 0.50),
			"p75": percentile(savings, 0.75),
			"p90": percentile(savings, 0.90),
		},
		QMeanStore: qStore / float64(cfg.EvalEpisodes),
		QMeanGC:    qGC / float64(cfg.EvalEpisodes),
		QMeanScan:  qScan / float64(cfg.EvalEpisodes),
		BMeanStore: bStore / float64(cfg.EvalEpisodes),
		BMeanGC:    bGC / float64(cfg.EvalEpisodes),
		BMeanScan:  bScan / float64(cfg.EvalEpisodes),
	}
}

func applySafetyGate(gate SafetyGateConfig, obs Observation, action int) int {
	if !gate.Enabled {
		return action
	}
	if obs.Rho >= gate.EmergencyRho {
		return actionTriggerGC
	}
	if action == actionTriggerGC && obs.Rho < gate.DeltaE && obs.CHat < gate.DeltaB {
		return actionWait
	}
	return action
}

func loadConfig(path string) (Config, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return Config{}, err
	}
	buf, err := os.ReadFile(absPath)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(buf, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	cfg.configDir = filepath.Dir(absPath)
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.TrainEpisodes <= 0 {
		return errors.New("trainEpisodes must be > 0")
	}
	if c.EvalEpisodes <= 0 {
		return errors.New("evalEpisodes must be > 0")
	}
	if c.Horizon <= 0 {
		return errors.New("horizon must be > 0")
	}
	if c.Epsilon <= 0 {
		return errors.New("epsilon must be > 0")
	}
	if c.EMAAlpha <= 0 || c.EMAAlpha > 1 {
		return errors.New("emaAlpha must be in (0,1]")
	}
	if c.Learning.Eta <= 0 || c.Learning.Eta > 1 {
		return errors.New("learning.eta must be in (0,1]")
	}
	if c.Learning.Gamma < 0 || c.Learning.Gamma >= 1 {
		return errors.New("learning.gamma must be in [0,1)")
	}
	if c.Learning.EpsilonStart <= 0 || c.Learning.EpsilonStart > 1 {
		return errors.New("learning.epsilonStart must be in (0,1]")
	}
	if c.Learning.EpsilonMin < 0 || c.Learning.EpsilonMin > c.Learning.EpsilonStart {
		return errors.New("learning.epsilonMin must be in [0, epsilonStart]")
	}
	if c.Learning.EpsilonDecay <= 0 || c.Learning.EpsilonDecay > 1 {
		return errors.New("learning.epsilonDecay must be in (0,1]")
	}
	if c.Cost.StorePricePerByte <= 0 {
		return errors.New("cost.storePricePerByte must be > 0")
	}
	if c.Cost.ComputePricePerSec <= 0 {
		return errors.New("cost.computePricePerSec must be > 0")
	}
	if c.Cost.RequestPrice <= 0 {
		return errors.New("cost.requestPrice must be > 0")
	}
	if c.Cost.BandwidthPricePerByte <= 0 {
		return errors.New("cost.bandwidthPricePerByte must be > 0")
	}
	if c.Cost.ScanPricePerByte <= 0 {
		return errors.New("cost.scanPricePerByte must be > 0")
	}
	if c.Cost.WS < 0 || c.Cost.WG < 0 || c.Cost.WR < 0 {
		return errors.New("cost weights wS/wG/wR must be >= 0")
	}
	if c.Environment.InitialLiveBytes <= 0 {
		return errors.New("environment.initialLiveBytes must be > 0")
	}
	if c.Environment.InitialGarbageBytes < 0 {
		return errors.New("environment.initialGarbageBytes must be >= 0")
	}
	if c.Environment.IngestBandwidthBytesPerStep <= 0 {
		return errors.New("environment.ingestBandwidthBytesPerStep must be > 0")
	}
	if c.Environment.WriteBytesMin <= 0 || c.Environment.WriteBytesMax < c.Environment.WriteBytesMin {
		return errors.New("environment.writeBytes range invalid")
	}
	if c.Environment.ReadBytesMin <= 0 || c.Environment.ReadBytesMax < c.Environment.ReadBytesMin {
		return errors.New("environment.readBytes range invalid")
	}
	if c.Environment.InvalidationRatioMin < 0 || c.Environment.InvalidationRatioMax > 1 || c.Environment.InvalidationRatioMax < c.Environment.InvalidationRatioMin {
		return errors.New("environment.invalidationRatio range invalid")
	}
	if c.Environment.RemoteScanBaseMin < 0 || c.Environment.RemoteScanBaseMax < c.Environment.RemoteScanBaseMin {
		return errors.New("environment.remoteScanBase range invalid")
	}
	if c.GC.BudgetPerStep <= 0 {
		return errors.New("gc.budgetPerStep must be > 0")
	}
	if c.GC.WindowH <= 0 {
		return errors.New("gc.windowH must be > 0")
	}
	if c.GC.CandidateCountMin <= 0 || c.GC.CandidateCountMax < c.GC.CandidateCountMin {
		return errors.New("gc.candidateCount range invalid")
	}
	if c.GC.CandidateSizeMinBytes <= 0 || c.GC.CandidateSizeMaxBytes < c.GC.CandidateSizeMinBytes {
		return errors.New("gc.candidateSize range invalid")
	}
	if c.Baseline.RhoThreshold < 0 || c.Baseline.RhoThreshold > 1 {
		return errors.New("baseline.rhoThreshold must be in [0,1]")
	}
	if c.Baseline.CHatThreshold < 0 || c.Baseline.RHatThreshold < -1 {
		return errors.New("baseline thresholds are invalid")
	}
	if c.Safety.Enabled {
		if c.Safety.DeltaE < 0 || c.Safety.DeltaE > 1 {
			return errors.New("safetyGate.deltaE must be in [0,1]")
		}
		if c.Safety.EmergencyRho < 0 || c.Safety.EmergencyRho > 1 {
			return errors.New("safetyGate.emergencyRho must be in [0,1]")
		}
	}
	if c.Seed == 0 {
		return errors.New("seed must be non-zero")
	}
	if c.Discretizer.Tau1 == nil || c.Discretizer.Tau2 == nil || c.Discretizer.Tau3 == nil || c.Discretizer.Tau4 == nil || c.Discretizer.Tau5 == nil {
		return errors.New("discretizer tau1..tau5 are required")
	}
	return nil
}

func safeRatio(num, den, eps float64) float64 {
	return num / math.Max(eps, den)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	if p <= 0 {
		p = 0
	}
	if p >= 1 {
		p = 1
	}
	vals := append([]float64(nil), xs...)
	sort.Float64s(vals)
	idx := int(math.Ceil(p*float64(len(vals)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(vals) {
		idx = len(vals) - 1
	}
	return vals[idx]
}

func (s *Simulator) sampleUniform(min, max float64) float64 {
	if max <= min {
		return min
	}
	return min + s.rng.Float64()*(max-min)
}

func actionName(a int) string {
	if a == actionTriggerGC {
		return "trigger_gc"
	}
	return "wait"
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <config-json-path>\n", filepath.Base(os.Args[0]))
		os.Exit(2)
	}
	cfg, err := loadConfig(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}
	encoder, err := NewEncoder(cfg.Discretizer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discretizer error: %v\n", err)
		os.Exit(1)
	}

	agent := NewAgent(encoder.StateCount(), cfg.Learning, cfg.Seed+2026)
	runTraining(cfg, encoder, agent)
	summary := runEvaluation(cfg, encoder, agent)

	fmt.Printf("q_learning_gc_simulation\n")
	fmt.Printf("state_space=%d action_space=%d q_table=%d\n", encoder.StateCount(), numActions, encoder.StateCount()*numActions)
	fmt.Printf("actions={%s,%s}\n", actionName(actionWait), actionName(actionTriggerGC))
	fmt.Printf("eval_episodes=%d horizon=%d\n", cfg.EvalEpisodes, cfg.Horizon)
	fmt.Printf("q_learning.mean_cost=%.6f\n", summary.QMeanCost)
	fmt.Printf("baseline.mean_cost=%.6f\n", summary.BaselineMeanCost)
	fmt.Printf("mean_savings=%.6f\n", summary.MeanSavings)
	fmt.Printf("mean_savings_pct=%.4f\n", summary.MeanSavingsPct*100)
	fmt.Printf("savings_p50=%.6f savings_p75=%.6f savings_p90=%.6f\n", summary.PSavings["p50"], summary.PSavings["p75"], summary.PSavings["p90"])
	fmt.Printf("q_cost_breakdown.store=%.6f gc=%.6f scan=%.6f\n", summary.QMeanStore, summary.QMeanGC, summary.QMeanScan)
	fmt.Printf("baseline_cost_breakdown.store=%.6f gc=%.6f scan=%.6f\n", summary.BMeanStore, summary.BMeanGC, summary.BMeanScan)
}
