package services

import "sort"

func buildWorldTimeAliasMap() map[string]string {
	m := map[string]string{}
	add := func(timezone string, aliases ...string) {
		addWorldTimeAliases(m, timezone, aliases...)
	}

	// 中国及港澳台
	add("Asia/Shanghai",
		"中国", "中国大陆", "大陆", "内地", "北京时间", "中国时间",
		"北京", "上海", "广州", "深圳", "杭州", "南京", "苏州", "无锡", "宁波", "温州",
		"天津", "重庆", "武汉", "成都", "西安", "郑州", "长沙", "南昌", "合肥", "福州",
		"厦门", "济南", "青岛", "大连", "沈阳", "长春", "哈尔滨", "石家庄", "太原", "呼和浩特",
		"兰州", "西宁", "银川", "乌鲁木齐", "拉萨", "昆明", "贵阳", "南宁", "海口", "三亚",
		"北京时区", "中国东八区", "北京时间现在",
		"china", "mainland china", "beijing", "shanghai", "guangzhou", "shenzhen",
		"hangzhou", "nanjing", "suzhou", "tianjin", "chongqing", "wuhan",
		"chengdu", "xian", "zhengzhou", "changsha", "xiamen", "qingdao",
		"shenyang", "harbin", "kunming", "urumqi",
	)
	add("Asia/Hong_Kong",
		"香港", "香港时间", "港区", "hk", "hong kong", "hongkong",
	)
	add("Asia/Macau",
		"澳门", "澳门时间", "澳門", "macao", "macau",
	)
	add("Asia/Taipei",
		"台湾", "台北", "高雄", "台中", "台南", "新竹", "台北时间", "台湾时间",
		"taiwan", "taipei", "kaohsiung", "taichung",
	)

	// 东亚
	add("Asia/Tokyo",
		"日本", "东京", "大阪", "京都", "名古屋", "札幌", "福冈", "冲绳", "日本时间", "东京时间",
		"japan", "tokyo", "osaka", "kyoto", "nagoya", "sapporo", "fukuoka", "okinawa",
	)
	add("Asia/Seoul",
		"韩国", "首尔", "釜山", "仁川", "大邱", "韩国时间", "首尔时间",
		"south korea", "korea", "seoul", "busan", "incheon", "daegu",
	)
	add("Asia/Pyongyang",
		"朝鲜", "平壤", "北朝鲜", "north korea", "pyongyang",
	)
	add("Asia/Ulaanbaatar",
		"蒙古", "乌兰巴托", "蒙古国", "mongolia", "ulaanbaatar",
	)

	// 东南亚
	add("Asia/Singapore",
		"新加坡", "狮城", "新加坡时间", "singapore", "sg",
	)
	add("Asia/Bangkok",
		"泰国", "曼谷", "芭提雅", "清迈", "泰国时间", "bangkok", "thailand", "chiang mai", "pattaya",
	)
	add("Asia/Ho_Chi_Minh",
		"越南", "河内", "胡志明", "胡志明市", "岘港", "越南时间",
		"vietnam", "hanoi", "ho chi minh", "ho chi minh city", "da nang",
	)
	add("Asia/Phnom_Penh",
		"柬埔寨", "金边", "柬埔寨时间", "cambodia", "phnom penh",
	)
	add("Asia/Vientiane",
		"老挝", "万象", "老挝时间", "laos", "vientiane",
	)
	add("Asia/Yangon",
		"缅甸", "仰光", "内比都", "缅甸时间", "myanmar", "yangon", "naypyidaw",
	)
	add("Asia/Kuala_Lumpur",
		"马来西亚", "吉隆坡", "槟城", "新山", "马来西亚时间", "malaysia", "kuala lumpur", "penang", "johor bahru",
	)
	add("Asia/Brunei",
		"文莱", "斯里巴加湾", "文莱时间", "brunei", "bandar seri begawan",
	)
	add("Asia/Jakarta",
		"印度尼西亚", "印尼", "雅加达", "爪哇", "苏门答腊", "西印尼时间",
		"indonesia", "jakarta", "java", "sumatra", "wib",
	)
	add("Asia/Makassar",
		"巴厘岛", "巴厘", "望加锡", "中印尼时间", "bali", "makassar", "wita",
	)
	add("Asia/Jayapura",
		"巴布亚", "查亚普拉", "东印尼时间", "papua", "jayapura", "wit",
	)
	add("Asia/Manila",
		"菲律宾", "马尼拉", "宿务", "达沃", "菲律宾时间", "philippines", "manila", "cebu", "davao",
	)
	add("Asia/Dili",
		"东帝汶", "帝力", "东帝汶时间", "timor leste", "dili",
	)

	// 南亚和中亚
	add("Asia/Kolkata",
		"印度", "新德里", "孟买", "班加罗尔", "加尔各答", "金奈", "海得拉巴", "印度时间",
		"india", "new delhi", "mumbai", "bangalore", "kolkata", "chennai", "hyderabad", "ist",
	)
	add("Asia/Kathmandu",
		"尼泊尔", "加德满都", "尼泊尔时间", "nepal", "kathmandu",
	)
	add("Asia/Colombo",
		"斯里兰卡", "科伦坡", "斯里兰卡时间", "sri lanka", "colombo",
	)
	add("Asia/Karachi",
		"巴基斯坦", "卡拉奇", "伊斯兰堡", "巴基斯坦时间", "pakistan", "karachi", "islamabad",
	)
	add("Asia/Dhaka",
		"孟加拉国", "达卡", "孟加拉时间", "bangladesh", "dhaka",
	)
	add("Asia/Kabul",
		"阿富汗", "喀布尔", "阿富汗时间", "afghanistan", "kabul",
	)
	add("Asia/Tashkent",
		"乌兹别克斯坦", "塔什干", "撒马尔罕", "uzbekistan", "tashkent", "samarkand",
	)
	add("Asia/Almaty",
		"哈萨克斯坦", "阿拉木图", "阿斯塔纳", "哈萨克时间", "kazakhstan", "almaty", "astana", "nur sultan",
	)
	add("Asia/Bishkek",
		"吉尔吉斯斯坦", "比什凯克", "kyrgyzstan", "bishkek",
	)
	add("Asia/Dushanbe",
		"塔吉克斯坦", "杜尚别", "tajikistan", "dushanbe",
	)
	add("Asia/Ashgabat",
		"土库曼斯坦", "阿什哈巴德", "turkmenistan", "ashgabat",
	)

	// 中东
	add("Asia/Dubai",
		"阿联酋", "迪拜", "阿布扎比", "沙迦", "阿联酋时间",
		"uae", "united arab emirates", "dubai", "abu dhabi", "sharjah",
	)
	add("Asia/Riyadh",
		"沙特", "沙特阿拉伯", "利雅得", "吉达", "麦加", "麦地那",
		"saudi", "saudi arabia", "riyadh", "jeddah", "mecca", "medina",
	)
	add("Asia/Qatar",
		"卡塔尔", "多哈", "qatar", "doha",
	)
	add("Asia/Kuwait",
		"科威特", "科威特城", "kuwait", "kuwait city",
	)
	add("Asia/Bahrain",
		"巴林", "麦纳麦", "bahrain", "manama",
	)
	add("Asia/Muscat",
		"阿曼", "马斯喀特", "oman", "muscat",
	)
	add("Asia/Tehran",
		"伊朗", "德黑兰", "iran", "tehran",
	)
	add("Asia/Baghdad",
		"伊拉克", "巴格达", "iraq", "baghdad",
	)
	add("Asia/Jerusalem",
		"以色列", "耶路撒冷", "特拉维夫", "israel", "jerusalem", "tel aviv", "telaviv",
	)
	add("Asia/Amman",
		"约旦", "安曼", "jordan", "amman",
	)
	add("Asia/Beirut",
		"黎巴嫩", "贝鲁特", "lebanon", "beirut",
	)
	add("Asia/Damascus",
		"叙利亚", "大马士革", "syria", "damascus",
	)
	add("Europe/Istanbul",
		"土耳其", "伊斯坦布尔", "安卡拉", "turkey", "istanbul", "ankara",
	)
	add("Asia/Tbilisi",
		"格鲁吉亚", "第比利斯", "georgia", "tbilisi",
	)
	add("Asia/Yerevan",
		"亚美尼亚", "埃里温", "armenia", "yerevan",
	)
	add("Asia/Baku",
		"阿塞拜疆", "巴库", "azerbaijan", "baku",
	)
	add("Europe/Nicosia",
		"塞浦路斯", "尼科西亚", "cyprus", "nicosia",
	)

	// 欧洲
	add("Europe/London",
		"英国", "伦敦", "曼彻斯特", "伯明翰", "利物浦", "苏格兰", "英格兰",
		"uk", "united kingdom", "britain", "great britain", "london", "manchester", "birmingham", "liverpool",
	)
	add("Europe/Dublin",
		"爱尔兰", "都柏林", "ireland", "dublin",
	)
	add("Europe/Paris",
		"法国", "巴黎", "里昂", "马赛", "france", "paris", "lyon", "marseille",
	)
	add("Europe/Berlin",
		"德国", "柏林", "法兰克福", "慕尼黑", "汉堡", "germany", "berlin", "frankfurt", "munich", "hamburg",
	)
	add("Europe/Amsterdam",
		"荷兰", "阿姆斯特丹", "netherlands", "amsterdam",
	)
	add("Europe/Brussels",
		"比利时", "布鲁塞尔", "belgium", "brussels",
	)
	add("Europe/Luxembourg",
		"卢森堡", "luxembourg",
	)
	add("Europe/Zurich",
		"瑞士", "苏黎世", "日内瓦", "switzerland", "zurich", "geneva",
	)
	add("Europe/Vienna",
		"奥地利", "维也纳", "austria", "vienna",
	)
	add("Europe/Rome",
		"意大利", "罗马", "米兰", "佛罗伦萨", "那不勒斯", "italy", "rome", "milan", "florence", "naples",
	)
	add("Europe/Madrid",
		"西班牙", "马德里", "巴塞罗那", "瓦伦西亚", "spain", "madrid", "barcelona", "valencia",
	)
	add("Europe/Lisbon",
		"葡萄牙", "里斯本", "波尔图", "portugal", "lisbon", "porto",
	)
	add("Europe/Copenhagen",
		"丹麦", "哥本哈根", "denmark", "copenhagen",
	)
	add("Europe/Oslo",
		"挪威", "奥斯陆", "norway", "oslo",
	)
	add("Europe/Stockholm",
		"瑞典", "斯德哥尔摩", "sweden", "stockholm",
	)
	add("Europe/Helsinki",
		"芬兰", "赫尔辛基", "finland", "helsinki",
	)
	add("Europe/Warsaw",
		"波兰", "华沙", "poland", "warsaw",
	)
	add("Europe/Prague",
		"捷克", "布拉格", "czech", "czech republic", "prague",
	)
	add("Europe/Bratislava",
		"斯洛伐克", "布拉迪斯拉发", "slovakia", "bratislava",
	)
	add("Europe/Budapest",
		"匈牙利", "布达佩斯", "hungary", "budapest",
	)
	add("Europe/Bucharest",
		"罗马尼亚", "布加勒斯特", "romania", "bucharest",
	)
	add("Europe/Sofia",
		"保加利亚", "索菲亚", "bulgaria", "sofia",
	)
	add("Europe/Athens",
		"希腊", "雅典", "greece", "athens",
	)
	add("Europe/Zagreb",
		"克罗地亚", "萨格勒布", "croatia", "zagreb",
	)
	add("Europe/Belgrade",
		"塞尔维亚", "贝尔格莱德", "serbia", "belgrade",
	)
	add("Europe/Sarajevo",
		"波黑", "波斯尼亚", "萨拉热窝", "bosnia", "bosnia and herzegovina", "sarajevo",
	)
	add("Europe/Ljubljana",
		"斯洛文尼亚", "卢布尔雅那", "slovenia", "ljubljana",
	)
	add("Europe/Skopje",
		"北马其顿", "斯科普里", "north macedonia", "skopje",
	)
	add("Europe/Tirane",
		"阿尔巴尼亚", "地拉那", "albania", "tirana",
	)
	add("Europe/Podgorica",
		"黑山", "波德戈里察", "montenegro", "podgorica",
	)
	add("Europe/Pristina",
		"科索沃", "普里什蒂纳", "kosovo", "pristina",
	)
	add("Europe/Chisinau",
		"摩尔多瓦", "基希讷乌", "moldova", "chisinau",
	)
	add("Europe/Minsk",
		"白俄罗斯", "明斯克", "belarus", "minsk",
	)
	add("Europe/Kyiv",
		"乌克兰", "基辅", "敖德萨", "ukraine", "kyiv", "kiev", "odessa",
	)
	add("Europe/Vilnius",
		"立陶宛", "维尔纽斯", "lithuania", "vilnius",
	)
	add("Europe/Riga",
		"拉脱维亚", "里加", "latvia", "riga",
	)
	add("Europe/Tallinn",
		"爱沙尼亚", "塔林", "estonia", "tallinn",
	)
	add("Atlantic/Reykjavik",
		"冰岛", "雷克雅未克", "iceland", "reykjavik",
	)
	add("Europe/Valletta",
		"马耳他", "瓦莱塔", "malta", "valletta",
	)
	add("Europe/Moscow",
		"俄罗斯", "莫斯科", "圣彼得堡", "喀山", "下诺夫哥罗德",
		"russia", "moscow", "saint petersburg", "st petersburg", "kazan",
	)
	add("Europe/Samara",
		"萨马拉", "samara",
	)
	add("Asia/Yekaterinburg",
		"叶卡捷琳堡", "yekaterinburg",
	)
	add("Asia/Novosibirsk",
		"新西伯利亚", "novosibirsk",
	)
	add("Asia/Vladivostok",
		"符拉迪沃斯托克", "海参崴", "vladivostok",
	)
	add("Asia/Kamchatka",
		"堪察加", "kamchatka",
	)

	// 非洲
	add("Africa/Cairo",
		"埃及", "开罗", "egypt", "cairo",
	)
	add("Africa/Casablanca",
		"摩洛哥", "卡萨布兰卡", "马拉喀什", "morocco", "casablanca", "marrakesh",
	)
	add("Africa/Algiers",
		"阿尔及利亚", "阿尔及尔", "algeria", "algiers",
	)
	add("Africa/Tunis",
		"突尼斯", "突尼斯市", "tunisia", "tunis",
	)
	add("Africa/Tripoli",
		"利比亚", "的黎波里", "libya", "tripoli",
	)
	add("Africa/Khartoum",
		"苏丹", "喀土穆", "sudan", "khartoum",
	)
	add("Africa/Juba",
		"南苏丹", "朱巴", "south sudan", "juba",
	)
	add("Africa/Johannesburg",
		"南非", "约翰内斯堡", "开普敦", "比勒陀利亚", "德班",
		"south africa", "johannesburg", "cape town", "pretoria", "durban",
	)
	add("Africa/Windhoek",
		"纳米比亚", "温得和克", "namibia", "windhoek",
	)
	add("Africa/Gaborone",
		"博茨瓦纳", "哈博罗内", "botswana", "gaborone",
	)
	add("Africa/Harare",
		"津巴布韦", "哈拉雷", "zimbabwe", "harare",
	)
	add("Africa/Lusaka",
		"赞比亚", "卢萨卡", "zambia", "lusaka",
	)
	add("Africa/Maputo",
		"莫桑比克", "马普托", "mozambique", "maputo",
	)
	add("Africa/Lagos",
		"尼日利亚", "拉各斯", "阿布贾", "喀麦隆", "雅温得", "尼日尔", "尼亚美", "贝宁", "多哥",
		"nigeria", "lagos", "abuja", "cameroon", "yaounde", "niger", "niamey", "benin", "togo",
	)
	add("Africa/Nairobi",
		"肯尼亚", "内罗毕", "乌干达", "坎帕拉", "坦桑尼亚", "达累斯萨拉姆",
		"kenya", "nairobi", "uganda", "kampala", "tanzania", "dar es salaam",
	)
	add("Africa/Addis_Ababa",
		"埃塞俄比亚", "亚的斯亚贝巴", "厄立特里亚", "阿斯马拉", "索马里", "摩加迪沙", "吉布提",
		"ethiopia", "addis ababa", "eritrea", "asmara", "somalia", "mogadishu", "djibouti",
	)
	add("Africa/Kigali",
		"卢旺达", "基加利", "rwanda", "kigali",
	)
	add("Africa/Bujumbura",
		"布隆迪", "布琼布拉", "burundi", "bujumbura",
	)
	add("Africa/Accra",
		"加纳", "阿克拉", "ghana", "accra",
	)
	add("Africa/Abidjan",
		"科特迪瓦", "象牙海岸", "阿比让", "ivory coast", "cote d'ivoire", "abidjan",
	)
	add("Africa/Dakar",
		"塞内加尔", "达喀尔", "senegal", "dakar",
	)
	add("Africa/Bamako",
		"马里", "巴马科", "mali", "bamako",
	)
	add("Africa/Conakry",
		"几内亚", "科纳克里", "guinea", "conakry",
	)
	add("Africa/Bissau",
		"几内亚比绍", "比绍", "guinea bissau", "bissau",
	)
	add("Africa/Monrovia",
		"利比里亚", "蒙罗维亚", "liberia", "monrovia",
	)
	add("Africa/Freetown",
		"塞拉利昂", "弗里敦", "sierra leone", "freetown",
	)
	add("Africa/Ceuta",
		"休达", "ceuta",
	)
	add("Atlantic/Cape_Verde",
		"佛得角", "普拉亚", "cape verde", "praia",
	)
	add("Africa/Libreville",
		"加蓬", "利伯维尔", "gabon", "libreville",
	)
	add("Africa/Malabo",
		"赤道几内亚", "马拉博", "equatorial guinea", "malabo",
	)
	add("Africa/Bangui",
		"中非", "中非共和国", "班吉", "central african republic", "bangui",
	)
	add("Africa/Kinshasa",
		"刚果金西部", "金沙萨", "kinshasa",
	)
	add("Africa/Lubumbashi",
		"刚果金东部", "卢本巴希", "lubumbashi",
	)
	add("Africa/Brazzaville",
		"刚果布", "布拉柴维尔", "congo", "republic of the congo", "brazzaville",
	)
	add("Africa/Luanda",
		"安哥拉", "罗安达", "angola", "luanda",
	)
	add("Indian/Antananarivo",
		"马达加斯加", "塔那那利佛", "madagascar", "antananarivo",
	)
	add("Indian/Mauritius",
		"毛里求斯", "路易港", "mauritius", "port louis",
	)
	add("Indian/Mahe",
		"塞舌尔", "马埃", "seychelles", "mahe",
	)
	add("Indian/Comoro",
		"科摩罗", "comoros", "comoro",
	)
	add("Indian/Reunion",
		"留尼汪", "reunion",
	)

	// 北美与中美洲
	add("America/New_York",
		"美国", "美东", "美国东部", "纽约", "华盛顿", "波士顿", "迈阿密", "亚特兰大",
		"usa", "us", "u.s.", "united states", "east coast", "eastern", "eastern time",
		"new york", "washington", "boston", "miami", "atlanta", "est",
	)
	add("America/Chicago",
		"美国中部", "美中", "芝加哥", "休斯敦", "达拉斯", "明尼阿波利斯", "新奥尔良",
		"central time", "central", "chicago", "houston", "dallas", "minneapolis", "new orleans", "cst",
	)
	add("America/Denver",
		"美国山地", "美山", "丹佛", "凤凰城", "盐湖城", "阿尔伯克基",
		"mountain time", "mountain", "denver", "phoenix", "salt lake city", "mst",
	)
	add("America/Los_Angeles",
		"美国西部", "美西", "洛杉矶", "旧金山", "西雅图", "拉斯维加斯", "圣地亚哥",
		"pacific time", "pacific", "los angeles", "san francisco", "seattle", "las vegas", "san diego", "pst",
	)
	add("America/Anchorage",
		"阿拉斯加", "安克雷奇", "alaska", "anchorage",
	)
	add("Pacific/Honolulu",
		"夏威夷", "火奴鲁鲁", "honolulu", "hawaii", "hst",
	)
	add("America/Toronto",
		"加拿大", "多伦多", "渥太华", "蒙特利尔", "canada", "toronto", "ottawa", "montreal",
	)
	add("America/Vancouver",
		"温哥华", "加拿大西部", "vancouver", "canada west",
	)
	add("America/Edmonton",
		"埃德蒙顿", "卡尔加里", "edmonton", "calgary",
	)
	add("America/Winnipeg",
		"温尼伯", "winnipeg",
	)
	add("America/Halifax",
		"哈利法克斯", "新斯科舍", "halifax", "nova scotia",
	)
	add("America/St_Johns",
		"圣约翰斯", "纽芬兰", "st johns", "newfoundland",
	)
	add("America/Mexico_City",
		"墨西哥", "墨西哥城", "mexico", "mexico city",
	)
	add("America/Tijuana",
		"蒂华纳", "tijuana",
	)
	add("America/Cancun",
		"坎昆", "cancun",
	)
	add("America/Guatemala",
		"危地马拉", "guatemala",
	)
	add("America/Belize",
		"伯利兹", "belize",
	)
	add("America/Tegucigalpa",
		"洪都拉斯", "特古西加尔巴", "honduras", "tegucigalpa",
	)
	add("America/Managua",
		"尼加拉瓜", "马那瓜", "nicaragua", "managua",
	)
	add("America/Costa_Rica",
		"哥斯达黎加", "圣何塞哥斯达黎加", "costa rica", "san jose costa rica",
	)
	add("America/Panama",
		"巴拿马", "panama", "panama city",
	)
	add("America/El_Salvador",
		"萨尔瓦多", "圣萨尔瓦多", "el salvador", "san salvador",
	)
	add("America/Havana",
		"古巴", "哈瓦那", "cuba", "havana",
	)
	add("America/Santo_Domingo",
		"多米尼加", "圣多明各", "dominican republic", "santo domingo",
	)
	add("America/Puerto_Rico",
		"波多黎各", "圣胡安", "puerto rico", "san juan",
	)
	add("America/Jamaica",
		"牙买加", "金斯敦", "jamaica", "kingston",
	)
	add("America/Port-au-Prince",
		"海地", "太子港", "haiti", "port au prince", "port-au-prince",
	)
	add("America/Nassau",
		"巴哈马", "拿骚", "bahamas", "nassau",
	)
	add("America/Barbados",
		"巴巴多斯", "布里奇敦", "barbados", "bridgetown",
	)
	add("America/Port_of_Spain",
		"特立尼达和多巴哥", "西班牙港", "trinidad and tobago", "port of spain",
	)

	// 南美洲
	add("America/Sao_Paulo",
		"巴西", "圣保罗", "里约", "巴西利亚", "巴西时间",
		"brazil", "sao paulo", "rio", "rio de janeiro", "brasilia",
	)
	add("America/Manaus",
		"马瑙斯", "亚马逊", "manaus", "amazonas",
	)
	add("America/Argentina/Buenos_Aires",
		"阿根廷", "布宜诺斯艾利斯", "argentina", "buenos aires",
	)
	add("America/Santiago",
		"智利", "圣地亚哥智利", "chile", "santiago chile",
	)
	add("America/Lima",
		"秘鲁", "利马", "peru", "lima",
	)
	add("America/Bogota",
		"哥伦比亚", "波哥大", "colombia", "bogota",
	)
	add("America/Caracas",
		"委内瑞拉", "加拉加斯", "venezuela", "caracas",
	)
	add("America/Guayaquil",
		"厄瓜多尔", "基多", "瓜亚基尔", "ecuador", "quito", "guayaquil",
	)
	add("America/La_Paz",
		"玻利维亚", "拉巴斯", "bolivia", "la paz",
	)
	add("America/Asuncion",
		"巴拉圭", "亚松森", "paraguay", "asuncion",
	)
	add("America/Montevideo",
		"乌拉圭", "蒙得维的亚", "uruguay", "montevideo",
	)
	add("America/Guyana",
		"圭亚那", "乔治敦圭亚那", "guyana", "georgetown guyana",
	)
	add("America/Paramaribo",
		"苏里南", "帕拉马里博", "suriname", "paramaribo",
	)
	add("America/Cayenne",
		"法属圭亚那", "卡宴", "french guiana", "cayenne",
	)

	// 大洋洲
	add("Australia/Sydney",
		"澳大利亚", "悉尼", "堪培拉", "新南威尔士", "australia", "sydney", "canberra", "nsw",
	)
	add("Australia/Melbourne",
		"墨尔本", "维多利亚州", "melbourne", "victoria australia",
	)
	add("Australia/Brisbane",
		"布里斯班", "昆士兰", "brisbane", "queensland",
	)
	add("Australia/Adelaide",
		"阿德莱德", "南澳", "adelaide", "south australia",
	)
	add("Australia/Darwin",
		"达尔文", "北领地", "darwin", "northern territory",
	)
	add("Australia/Perth",
		"珀斯", "西澳", "perth", "western australia",
	)
	add("Australia/Hobart",
		"霍巴特", "塔斯马尼亚", "hobart", "tasmania",
	)
	add("Pacific/Auckland",
		"新西兰", "奥克兰", "惠灵顿", "基督城", "new zealand", "auckland", "wellington", "christchurch",
	)
	add("Pacific/Chatham",
		"查塔姆群岛", "chatham",
	)
	add("Pacific/Port_Moresby",
		"巴布亚新几内亚", "莫尔兹比港", "papua new guinea", "port moresby",
	)
	add("Pacific/Fiji",
		"斐济", "苏瓦", "fiji", "suva",
	)
	add("Pacific/Apia",
		"萨摩亚", "阿皮亚", "samoa", "apia",
	)
	add("Pacific/Tongatapu",
		"汤加", "努库阿洛法", "tonga", "nuku'alofa", "tongatapu",
	)
	add("Pacific/Efate",
		"瓦努阿图", "维拉港", "vanuatu", "port vila", "efate",
	)
	add("Pacific/Guadalcanal",
		"所罗门群岛", "霍尼亚拉", "solomon islands", "honiara", "guadalcanal",
	)
	add("Pacific/Noumea",
		"新喀里多尼亚", "努美阿", "new caledonia", "noumea",
	)
	add("Pacific/Guam",
		"关岛", "guam",
	)
	add("Pacific/Saipan",
		"塞班", "北马里亚纳", "saipan", "northern mariana",
	)
	add("Pacific/Tahiti",
		"塔希提", "法属波利尼西亚", "tahiti", "french polynesia",
	)
	add("Pacific/Palau",
		"帕劳", "palau",
	)
	add("Pacific/Majuro",
		"马绍尔群岛", "马朱罗", "marshall islands", "majuro",
	)
	add("Pacific/Tarawa",
		"基里巴斯", "塔拉瓦", "kiribati", "tarawa",
	)
	add("Pacific/Funafuti",
		"图瓦卢", "富纳富提", "tuvalu", "funafuti",
	)
	add("Pacific/Nauru",
		"瑙鲁", "nauru",
	)
	add("Pacific/Pohnpei",
		"密克罗尼西亚", "波纳佩", "micronesia", "pohnpei",
	)

	return m
}

func addWorldTimeAliases(dst map[string]string, timezone string, aliases ...string) {
	if !validTZ(timezone) {
		return
	}
	for _, alias := range aliases {
		key := normalizeRegionKey(alias)
		if key == "" {
			continue
		}
		if _, exists := dst[key]; exists {
			continue
		}
		dst[key] = timezone
	}
}

func buildWorldTimeLocalZones(aliasMap map[string]string) []string {
	set := map[string]struct{}{}
	for _, tz := range aliasMap {
		if validTZ(tz) {
			set[tz] = struct{}{}
		}
	}
	extras := []string{
		"UTC", "Etc/UTC", "Etc/GMT",
		"Europe/Kaliningrad", "Europe/Ulyanovsk", "Asia/Omsk", "Asia/Krasnoyarsk", "Asia/Irkutsk",
		"America/Adak", "America/Boise", "America/Detroit", "America/Indiana/Indianapolis",
		"America/Moncton", "America/Regina", "America/Whitehorse", "America/Yellowknife",
		"America/Araguaina", "America/Belem", "America/Campo_Grande", "America/Fortaleza",
		"America/Recife", "America/Porto_Velho", "America/Rio_Branco",
		"Africa/El_Aaiun", "Africa/Casablanca", "Africa/Douala", "Africa/Blantyre", "Africa/Maseru", "Africa/Mbabane",
		"Asia/Anadyr", "Asia/Magadan", "Asia/Sakhalin",
		"Pacific/Kiritimati", "Pacific/Niue", "Pacific/Pago_Pago", "Pacific/Kosrae",
		"Indian/Maldives", "Indian/Mayotte",
	}
	for _, tz := range extras {
		if validTZ(tz) {
			set[tz] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for tz := range set {
		out = append(out, tz)
	}
	sort.Strings(out)
	return out
}
