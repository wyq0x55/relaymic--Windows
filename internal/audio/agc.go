package audio

import "math"

// AGC 自动增益控制。
//
// 固定增益在这个产品里是行不通的：调小了，识别引擎的静音门限把人声当噪声滤掉；
// 调大了，说话稍响就削顶，波形被切平，听起来就是杂音。而每个发送端的麦克风
// 灵敏度、每个人说话的音量都不一样，没有哪个固定值是对的。
//
// 这里用经典的快压慢放：音量冲高时迅速收增益避免削顶，安静下来后缓慢放开，
// 避免把呼吸和底噪一并抬起来。最后再过一道硬限幅兜底。
type AGC struct {
	targetPeak float64 // 目标峰值（0~1），留足余量给限幅器
	maxGain    float64
	minGain    float64
	attack     float64 // 收增益的速度：要快，否则削顶已经发生了
	release    float64 // 放增益的速度：要慢，否则会听到"呼吸感"
	noiseFloor float64 // 低于此电平认为是静音，不参与增益计算
	gateFloor  float64 // 低于此电平判为纯底噪，压下去
	gateHold   int     // 判静后再保持开门多少帧，避免切掉词尾

	gain     float64
	holdLeft int
	gate     float64 // 平滑后的门系数，向目标值渐进，绝不跳变
	prevEff  float64 // 上一帧的最终系数，帧内逐样本从它滑向新值
}

// NewAGC 按语音输入场景给一组默认参数。
func NewAGC() *AGC {
	return &AGC{
		// 这几个阈值必须贴着实测来定。浏览器发来的语音峰值实测只有
		// -56dBFS 左右，比想当然的"正常电平"低一个数量级；按经验值拍
		// 阈值，会把正常人声整个判成静音掐掉。
		targetPeak: 0.4,    // -8 dBFS，识别引擎够用，留足余量给瞬态
		maxGain:    24,     // 输入只有 -56dBFS，要 27dB 才抬得到 -29dBFS
		minGain:    0.25,   // 也能压住过响的输入
		attack:     0.35,   // 一帧之内基本收到位
		release:    0.0025, // 约几秒钟才放满，听不出增益在动
		noiseFloor: 0.0003, // -70 dBFS：低于此才认定没信号，不参与增益计算
		gateFloor:  0.01,   // 判据是"增益之后"的电平，见 Process
		gateHold:   25,     // 语音停顿期间先别关门，约 500ms
		gain:       1.0,
		gate:       1.0,
	}
}

// Process 就地处理一帧交错 PCM。
func (a *AGC) Process(pcm []int16) {
	if len(pcm) == 0 {
		return
	}

	peak := 0.0
	for _, s := range pcm {
		v := float64(s)
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	peak /= math.MaxInt16

	// 静音段保持当前增益：既不抬底噪，也不在下一句话开头突然变响。
	if peak > a.noiseFloor {
		desired := a.targetPeak / peak
		if desired > a.maxGain {
			desired = a.maxGain
		}
		if desired < a.minGain {
			desired = a.minGain
		}
		rate := a.release
		if desired < a.gain {
			rate = a.attack
		}
		a.gain += (desired - a.gain) * rate
	}

	// 噪声门看的是"抬完之后"的电平，不是原始电平。
	//
	// 原始电平做判据是错的：输入本来就可能只有 -56dBFS，那是正常人声，
	// 不是底噪 —— 拿绝对阈值去卡，会把该放大的信号直接掐掉。
	// 抬完之后仍然很小的，才是真底噪。
	target := 1.0
	if peak*a.gain >= a.gateFloor {
		a.holdLeft = a.gateHold
	} else if a.holdLeft > 0 {
		a.holdLeft--
	} else {
		target = 0.2 // 衰减而非全静音。压得太深的代价在开门瞬间：
		// 字头声母落在爬坡窗口里会被衰减到糊掉，-14dB 已够压底噪
	}
	// 门系数只能渐变，不能跳变：一帧内从 0.08 跳到 1.0 是 +22dB 的
	// 台阶，听感就是每句话开头"啪"一声。开门快（两三帧到位，不吞字头），
	// 关门慢（底噪缓缓沉下去）。
	rate := 0.1
	if target > a.gate {
		rate = 0.7 // 开门要快：慢一帧，字头声母就多糊 20ms
	}
	a.gate += (target - a.gate) * rate

	// 增益必须在帧内逐样本渐变，不能整帧一个值。
	// 系数逐帧更新、帧内恒定的话，所有变化都落在 20ms 帧边界上 ——
	// attack 快收时相邻帧能差 2dB 以上，听感就是随语音节奏的"啪啪"声。
	// 从上一帧的终值线性滑到本帧的目标值，台阶就成了斜坡。
	effective := a.gain * a.gate
	if a.prevEff == 0 {
		a.prevEff = effective
	}
	step := (effective - a.prevEff) / float64(len(pcm))
	cur := a.prevEff
	for i, s := range pcm {
		cur += step
		v := float64(s) * cur
		// 限幅器：AGC 反应不过来的瞬态由它兜住，宁可压扁一两个样本，
		// 也不能让整数回绕 —— 那会变成刺耳的爆音。
		if v > math.MaxInt16 {
			v = math.MaxInt16
		} else if v < math.MinInt16 {
			v = math.MinInt16
		}
		pcm[i] = int16(v)
	}
	a.prevEff = effective
}

// Gain 返回当前增益，用于诊断输出。
func (a *AGC) Gain() float64 { return a.gain }
